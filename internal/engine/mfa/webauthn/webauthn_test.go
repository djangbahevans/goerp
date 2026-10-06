package webauthn

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/descope/virtualwebauthn"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtest"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/mfatest"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

const (
	testRPID   = "localhost"
	testOrigin = "http://localhost:8080"
	testRPName = "GoERP Test"
)

type testEnv struct {
	tenant  mfatest.Tenant
	service *Service
	conn    *sql.DB
	users   *user.Store
}

func openTestEnv(t *testing.T) *testEnv {
	t.Helper()

	ctx := t.Context()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})

	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}

	tt := mfatest.NewTenant(t, conn)
	mfaStore := mfa.NewStore(conn)
	if err := mfaStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
	}

	keys := authtest.RowKeys()

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}

	t.Cleanup(func() {
		_ = cacheClient.Close()
	})

	svc, err := NewService(Config{
		RPID:          testRPID,
		RPDisplayName: testRPName,
		RPOrigins:     []string{testOrigin},
	}, mfaStore, keys, cacheClient, session.NewStore(conn))
	if err != nil {
		t.Fatalf("NewService() error: %v", err)
	}

	return &testEnv{
		tenant:  tt,
		service: svc,
		conn:    conn,
		users:   userStore,
	}
}

func (e *testEnv) createUser(t *testing.T) string {
	t.Helper()

	email := fmt.Sprintf("webauthntest%d@example.com", time.Now().UnixNano())
	userID, err := e.users.FindOrCreateInvited(t.Context(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited(%q) error: %v", email, err)
	}

	t.Cleanup(func() {
		_, _ = e.conn.Exec("DELETE FROM system.users WHERE id = $1", userID)
	})
	mfatest.AddMember(t, e.conn, e.tenant, userID)
	return userID
}

func testRP() virtualwebauthn.RelyingParty {
	return virtualwebauthn.RelyingParty{ID: testRPID, Name: testRPName, Origin: testOrigin}
}

// register runs a full registration ceremony through a fresh virtual
// authenticator/credential, returning both for use in a following login.
func register(t *testing.T, env *testEnv, userID string) (virtualwebauthn.Authenticator, virtualwebauthn.Credential) {
	t.Helper()

	ctx := t.Context()

	optionsJSON, ceremonyID, err := env.service.BeginRegistration(ctx, userID, "user@example.com", mfa.Scope{TenantID: env.tenant.ID})
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

	if _, err := env.service.FinishRegistration(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON), nil, mfa.Scope{TenantID: env.tenant.ID}, env.sessionID(t, userID)); err != nil {
		t.Fatalf("FinishRegistration() error: %v", err)
	}

	return authenticator, credential
}

func TestRegistrationThenLogin_Succeeds(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	authenticator, credential := register(t, env, userID)
	ctx := t.Context()

	optionsJSON, ceremonyID, err := env.service.BeginLogin(ctx, userID, "user@example.com", mfa.Scope{TenantID: env.tenant.ID})
	if err != nil {
		t.Fatalf("BeginLogin() error: %v", err)
	}

	assertionOptions, err := virtualwebauthn.ParseAssertionOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAssertionOptions() error: %v", err)
	}

	credential.Counter++
	responseJSON := virtualwebauthn.CreateAssertionResponse(testRP(), authenticator, credential, *assertionOptions)

	credentialID, err := env.service.FinishLogin(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON), mfa.Scope{TenantID: env.tenant.ID})
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
	ctx := t.Context()

	// A real, successful login first, advancing the stored sign count.
	optionsJSON, ceremonyID, err := env.service.BeginLogin(ctx, userID, "user@example.com", mfa.Scope{TenantID: env.tenant.ID})
	if err != nil {
		t.Fatalf("BeginLogin() error: %v", err)
	}

	assertionOptions, err := virtualwebauthn.ParseAssertionOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAssertionOptions() error: %v", err)
	}

	credential.Counter = 5
	responseJSON := virtualwebauthn.CreateAssertionResponse(testRP(), authenticator, credential, *assertionOptions)
	credentialID, err := env.service.FinishLogin(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON), mfa.Scope{TenantID: env.tenant.ID})
	if err != nil {
		t.Fatalf("first FinishLogin() error: %v", err)
	}

	// A second "login" replaying a sign count that doesn't exceed the
	// now-stored value (5) — a cloned authenticator or a replayed
	// assertion, auth-internals.md §8's own scenario.
	optionsJSON2, ceremonyID2, err := env.service.BeginLogin(ctx, userID, "user@example.com", mfa.Scope{TenantID: env.tenant.ID})
	if err != nil {
		t.Fatalf("second BeginLogin() error: %v", err)
	}

	assertionOptions2, err := virtualwebauthn.ParseAssertionOptions(string(optionsJSON2))
	if err != nil {
		t.Fatalf("ParseAssertionOptions() error: %v", err)
	}

	credential.Counter = 3 // less than the stored 5
	replayResponseJSON := virtualwebauthn.CreateAssertionResponse(testRP(), authenticator, credential, *assertionOptions2)

	_, err = env.service.FinishLogin(ctx, userID, ceremonyID2, "user@example.com", []byte(replayResponseJSON), mfa.Scope{TenantID: env.tenant.ID})
	cloneErr, ok := errors.AsType[*CloneDetectedError](err)
	if !ok {
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

	_, err := env.service.FinishRegistration(t.Context(), userID, "not-a-real-ceremony-id", "user@example.com", []byte("{}"), nil, mfa.Scope{TenantID: env.tenant.ID}, env.sessionID(t, userID))
	if !errors.Is(err, ErrCeremonyExpired) {
		t.Errorf("FinishRegistration() error = %v, want ErrCeremonyExpired", err)
	}
}

func TestFinishRegistration_WrongUserRejected(t *testing.T) {
	env := openTestEnv(t)
	userA := env.createUser(t)
	userB := env.createUser(t)
	ctx := t.Context()

	_, ceremonyID, err := env.service.BeginRegistration(ctx, userA, "a@example.com", mfa.Scope{TenantID: env.tenant.ID})
	if err != nil {
		t.Fatalf("BeginRegistration() error: %v", err)
	}

	_, err = env.service.FinishRegistration(ctx, userB, ceremonyID, "b@example.com", []byte("{}"), nil, mfa.Scope{TenantID: env.tenant.ID}, env.sessionID(t, userB))
	if !errors.Is(err, ErrCeremonyUserMismatch) {
		t.Errorf("FinishRegistration() error = %v, want ErrCeremonyUserMismatch", err)
	}
}

func TestFinishRegistration_CeremonyIsSingleUse(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	ctx := t.Context()

	optionsJSON, ceremonyID, err := env.service.BeginRegistration(ctx, userID, "user@example.com", mfa.Scope{TenantID: env.tenant.ID})
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

	if _, err := env.service.FinishRegistration(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON), nil, mfa.Scope{TenantID: env.tenant.ID}, env.sessionID(t, userID)); err != nil {
		t.Fatalf("first FinishRegistration() error: %v", err)
	}

	_, err = env.service.FinishRegistration(ctx, userID, ceremonyID, "user@example.com", []byte(responseJSON), nil, mfa.Scope{TenantID: env.tenant.ID}, env.sessionID(t, userID))
	if !errors.Is(err, ErrCeremonyExpired) {
		t.Errorf("replayed FinishRegistration() error = %v, want ErrCeremonyExpired (session already consumed)", err)
	}
}

func TestBeginLogin_NoEnrolledCredentialsRejected(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	_, _, err := env.service.BeginLogin(t.Context(), userID, "user@example.com", mfa.Scope{TenantID: env.tenant.ID})
	if !errors.Is(err, ErrNoEnrolledCredentials) {
		t.Errorf("BeginLogin() error = %v, want ErrNoEnrolledCredentials", err)
	}
}

func TestBeginRegistration_ExcludesAlreadyEnrolledCredential(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	_, credential := register(t, env, userID)

	optionsJSON, _, err := env.service.BeginRegistration(t.Context(), userID, "user@example.com", mfa.Scope{TenantID: env.tenant.ID})
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

func (e *testEnv) sessionID(t *testing.T, userID string) string {
	t.Helper()

	store := session.NewStore(e.conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}

	var id string
	if err := e.conn.QueryRowContext(t.Context(), `SELECT id FROM system.sessions WHERE user_id=$1 AND tenant_id=$2 AND revoked_at IS NULL LIMIT 1`, userID, e.tenant.ID).Scan(&id); err == nil {
		return id
	}

	id = uuid.NewV7().String()
	if err := store.Insert(t.Context(), session.Row{ID: id, UserID: userID, TenantID: e.tenant.ID, DeviceID: uuid.NewV7().String(), RefreshHash: id, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = e.conn.Exec(`DELETE FROM system.sessions WHERE id=$1`, id)
	})
	return id
}

func TestFinishLoginConcurrentCompletionIsSingleUse(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	authenticator, credential := register(t, env, userID)
	scope := mfa.Scope{TenantID: env.tenant.ID}

	optionsJSON, ceremonyID, err := env.service.BeginLogin(t.Context(), userID, "user@example.com", scope)
	if err != nil {
		t.Fatal(err)
	}

	options, err := virtualwebauthn.ParseAssertionOptions(string(optionsJSON))
	if err != nil {
		t.Fatal(err)
	}

	credential.Counter = 1
	response := []byte(virtualwebauthn.CreateAssertionResponse(testRP(), authenticator, credential, *options))
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			_, err := env.service.FinishLogin(t.Context(), userID, ceremonyID, "user@example.com", response, scope)
			results <- err
		})
	}

	workers.Wait()
	close(results)

	var successful, expired int
	for err := range results {
		switch {
		case err == nil:
			successful++
		case errors.Is(err, ErrCeremonyExpired):
			expired++
		default:
			t.Fatalf("concurrent completion: %v", err)
		}
	}

	if successful != 1 || expired != 1 {
		t.Fatalf("successful/expired = %d/%d, want 1/1", successful, expired)
	}
}
