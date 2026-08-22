package webauthn

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

const (
	testRPID   = "localhost"
	testOrigin = "http://localhost:8080"
	testRPName = "GoERP Test"
)

// memoryBackend is an in-process secrets.Backend that supports Set,
// standing in for a real vault/aws_secretsmanager deployment — mirrors
// rowcrypt's/totp's own test convention.
type memoryBackend struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemoryBackend() *memoryBackend { return &memoryBackend{values: map[string]string{}} }

func (b *memoryBackend) Get(ctx context.Context, key string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.values[key], nil
}

func (b *memoryBackend) Set(ctx context.Context, key, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.values[key] = value
	return nil
}

func (b *memoryBackend) Rotate(ctx context.Context, key string) (string, error) {
	return "", secrets.ErrRotateNotSupported
}

// lockRowEncryptionKeysTable mirrors totp/rowcrypt's own lock helper —
// serializes this package's tests against every other package's test
// touching the shared system.row_encryption_keys table.
func lockRowEncryptionKeysTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	key := db.AdvisoryLockKey("test.row_encryption_keys_table")

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire dedicated connection for row-encryption-key lock: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatalf("acquire row-encryption-key advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		_ = conn.Close()
	})
}

type testEnv struct {
	service *Service
	conn    *sql.DB
	users   *user.Store
}

func openTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockRowEncryptionKeysTable(t, conn)
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.row_encryption_keys`) })

	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}

	mfaStore := mfa.NewStore(conn)
	if err := mfaStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
	}

	rowCryptStore := rowcrypt.NewStore(conn, newMemoryBackend())
	if err := rowCryptStore.Bootstrap(ctx); err != nil {
		t.Fatalf("rowcrypt Bootstrap() error: %v", err)
	}
	keys, err := rowCryptStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	svc, err := NewService(Config{
		RPID:          testRPID,
		RPDisplayName: testRPName,
		RPOrigins:     []string{testOrigin},
	}, mfaStore, keys, cacheClient)
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}

	return &testEnv{service: svc, conn: conn, users: userStore}
}

func (e *testEnv) createUser(t *testing.T) string {
	t.Helper()
	email := fmt.Sprintf("webauthntest%d@example.com", time.Now().UnixNano())
	userID, err := e.users.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited(%q) error: %v", email, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.users WHERE id = $1", userID) })
	return userID
}

func testRP() virtualwebauthn.RelyingParty {
	return virtualwebauthn.RelyingParty{ID: testRPID, Name: testRPName, Origin: testOrigin}
}

// register runs a full registration ceremony through a fresh virtual
// authenticator/credential, returning both for use in a following login.
func register(t *testing.T, env *testEnv, userID string) (virtualwebauthn.Authenticator, virtualwebauthn.Credential) {
	t.Helper()
	ctx := context.Background()

	optionsJSON, ceremonyID, err := env.service.BeginRegistration(ctx, userID, "user@example.com")
	if err != nil {
		t.Fatalf("BeginRegistration() error: %v", err)
	}

	attestationOptions, err := virtualwebauthn.ParseAttestationOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAttestationOptions() error: %v", err)
	}

	authenticator := virtualwebauthn.NewAuthenticator()
	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	authenticator.AddCredential(credential)

	responseJSON := virtualwebauthn.CreateAttestationResponse(testRP(), authenticator, credential, *attestationOptions)

	if _, err := env.service.FinishRegistration(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON), nil); err != nil {
		t.Fatalf("FinishRegistration() error: %v", err)
	}

	return authenticator, credential
}

func TestRegistrationThenLogin_Succeeds(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	authenticator, credential := register(t, env, userID)
	ctx := context.Background()

	optionsJSON, ceremonyID, err := env.service.BeginLogin(ctx, userID, "user@example.com")
	if err != nil {
		t.Fatalf("BeginLogin() error: %v", err)
	}

	assertionOptions, err := virtualwebauthn.ParseAssertionOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAssertionOptions() error: %v", err)
	}

	credential.Counter++
	responseJSON := virtualwebauthn.CreateAssertionResponse(testRP(), authenticator, credential, *assertionOptions)

	credentialID, err := env.service.FinishLogin(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON))
	if err != nil {
		t.Fatalf("FinishLogin() error: %v", err)
	}
	if credentialID == "" {
		t.Error("FinishLogin() returned empty credential id")
	}

	var lastUsedAt sql.NullTime
	if err := env.conn.QueryRowContext(ctx,
		"SELECT last_used_at FROM system.user_mfa WHERE id = $1", credentialID,
	).Scan(&lastUsedAt); err != nil {
		t.Fatalf("query last_used_at: %v", err)
	}
	if !lastUsedAt.Valid {
		t.Error("last_used_at is NULL, want set after a successful login")
	}
}

func TestFinishLogin_CloneOrReplayRevokesCredentialAndReturnsCloneDetectedError(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	authenticator, credential := register(t, env, userID)
	ctx := context.Background()

	// A real, successful login first, advancing the stored sign count.
	optionsJSON, ceremonyID, err := env.service.BeginLogin(ctx, userID, "user@example.com")
	if err != nil {
		t.Fatalf("BeginLogin() error: %v", err)
	}
	assertionOptions, err := virtualwebauthn.ParseAssertionOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAssertionOptions() error: %v", err)
	}
	credential.Counter = 5
	responseJSON := virtualwebauthn.CreateAssertionResponse(testRP(), authenticator, credential, *assertionOptions)
	credentialID, err := env.service.FinishLogin(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON))
	if err != nil {
		t.Fatalf("first FinishLogin() error: %v", err)
	}

	// A second "login" replaying a sign count that doesn't exceed the
	// now-stored value (5) — a cloned authenticator or a replayed
	// assertion, auth-internals.md §8's own scenario.
	optionsJSON2, ceremonyID2, err := env.service.BeginLogin(ctx, userID, "user@example.com")
	if err != nil {
		t.Fatalf("second BeginLogin() error: %v", err)
	}
	assertionOptions2, err := virtualwebauthn.ParseAssertionOptions(string(optionsJSON2))
	if err != nil {
		t.Fatalf("ParseAssertionOptions() error: %v", err)
	}
	credential.Counter = 3 // less than the stored 5
	replayResponseJSON := virtualwebauthn.CreateAssertionResponse(testRP(), authenticator, credential, *assertionOptions2)

	_, err = env.service.FinishLogin(ctx, userID, ceremonyID2, "user@example.com", []byte(replayResponseJSON))
	var cloneErr *CloneDetectedError
	if !errors.As(err, &cloneErr) {
		t.Fatalf("FinishLogin() error = %v, want *CloneDetectedError", err)
	}
	if cloneErr.CredentialID != credentialID {
		t.Errorf("CloneDetectedError.CredentialID = %q, want %q", cloneErr.CredentialID, credentialID)
	}

	var revokedAt sql.NullTime
	if err := env.conn.QueryRowContext(ctx,
		"SELECT revoked_at FROM system.user_mfa WHERE id = $1", credentialID,
	).Scan(&revokedAt); err != nil {
		t.Fatalf("query revoked_at: %v", err)
	}
	if !revokedAt.Valid {
		t.Error("revoked_at is NULL, want set after clone detection")
	}
}

func TestFinishRegistration_ExpiredOrUnknownCeremonyRejected(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	_, err := env.service.FinishRegistration(context.Background(), userID, "not-a-real-ceremony-id", "user@example.com", []byte("{}"), nil)
	if !errors.Is(err, ErrCeremonyExpired) {
		t.Errorf("FinishRegistration() error = %v, want ErrCeremonyExpired", err)
	}
}

func TestFinishRegistration_WrongUserRejected(t *testing.T) {
	env := openTestEnv(t)
	userA := env.createUser(t)
	userB := env.createUser(t)
	ctx := context.Background()

	_, ceremonyID, err := env.service.BeginRegistration(ctx, userA, "a@example.com")
	if err != nil {
		t.Fatalf("BeginRegistration() error: %v", err)
	}

	_, err = env.service.FinishRegistration(ctx, userB, ceremonyID, "b@example.com", []byte("{}"), nil)
	if !errors.Is(err, ErrCeremonyUserMismatch) {
		t.Errorf("FinishRegistration() error = %v, want ErrCeremonyUserMismatch", err)
	}
}

func TestFinishRegistration_CeremonyIsSingleUse(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	ctx := context.Background()

	optionsJSON, ceremonyID, err := env.service.BeginRegistration(ctx, userID, "user@example.com")
	if err != nil {
		t.Fatalf("BeginRegistration() error: %v", err)
	}
	attestationOptions, err := virtualwebauthn.ParseAttestationOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAttestationOptions() error: %v", err)
	}
	authenticator := virtualwebauthn.NewAuthenticator()
	credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	authenticator.AddCredential(credential)
	responseJSON := virtualwebauthn.CreateAttestationResponse(testRP(), authenticator, credential, *attestationOptions)

	if _, err := env.service.FinishRegistration(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON), nil); err != nil {
		t.Fatalf("first FinishRegistration() error: %v", err)
	}

	_, err = env.service.FinishRegistration(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON), nil)
	if !errors.Is(err, ErrCeremonyExpired) {
		t.Errorf("replayed FinishRegistration() error = %v, want ErrCeremonyExpired (session already consumed)", err)
	}
}

func TestBeginLogin_NoEnrolledCredentialsRejected(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	_, _, err := env.service.BeginLogin(context.Background(), userID, "user@example.com")
	if !errors.Is(err, ErrNoEnrolledCredentials) {
		t.Errorf("BeginLogin() error = %v, want ErrNoEnrolledCredentials", err)
	}
}

func TestBeginRegistration_ExcludesAlreadyEnrolledCredential(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	_, credential := register(t, env, userID)

	optionsJSON, _, err := env.service.BeginRegistration(context.Background(), userID, "user@example.com")
	if err != nil {
		t.Fatalf("BeginRegistration() error: %v", err)
	}
	attestationOptions, err := virtualwebauthn.ParseAttestationOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAttestationOptions() error: %v", err)
	}
	if len(attestationOptions.ExcludeCredentials) != 1 {
		t.Fatalf("ExcludeCredentials = %v, want exactly the one already-enrolled credential", attestationOptions.ExcludeCredentials)
	}
	wantID := base64.RawURLEncoding.EncodeToString(credential.ID)
	if attestationOptions.ExcludeCredentials[0] != wantID {
		t.Errorf("excluded credential id = %q, want %q", attestationOptions.ExcludeCredentials[0], wantID)
	}
}
