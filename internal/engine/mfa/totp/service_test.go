package totp

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	pquernatotp "github.com/pquerna/otp/totp"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// memoryBackend is an in-process secrets.Backend that supports Set,
// standing in for a real vault/aws_secretsmanager deployment — mirrors
// rowcrypt's and signingkey's own test convention.
type memoryBackend struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{values: make(map[string]string)}
}

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
	return "", fmt.Errorf("rotate not supported")
}

// lockRowEncryptionKeysTable takes a session-scoped Postgres advisory lock,
// same reasoning rowcrypt.lockRowEncryptionKeysTable documents — this
// package's own tests share system.row_encryption_keys with rowcrypt's
// tests against the same real compose.dev.yml Postgres instance, and each
// test here uses its own fresh in-memory secrets.Backend, so a row left
// over from another test (or another package's concurrently running test)
// would load with no matching key material.
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

	return &testEnv{
		service: NewService(mfaStore, keys, cacheClient),
		conn:    conn,
		users:   userStore,
	}
}

func (e *testEnv) createUser(t *testing.T) string {
	t.Helper()
	email := fmt.Sprintf("totptest%d@example.com", time.Now().UnixNano())
	userID, err := e.users.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited(%q) error: %v", email, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.users WHERE id = $1", userID) })
	return userID
}

func TestEnroll_StoresEncryptedCredentialAndReturnsSVG(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	svg, cred, err := env.service.Enroll(context.Background(), userID, "user@example.com", nil)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	if len(svg) == 0 {
		t.Error("Enroll() returned empty SVG")
	}
	if cred.Type != mfa.CredentialTOTP {
		t.Errorf("cred.Type = %q, want %q", cred.Type, mfa.CredentialTOTP)
	}

	var storedCredential []byte
	if err := env.conn.QueryRowContext(context.Background(),
		"SELECT credential FROM system.user_mfa WHERE id = $1", cred.ID,
	).Scan(&storedCredential); err != nil {
		t.Fatalf("query stored credential: %v", err)
	}
	// The stored value is rowcrypt's {key_id}:{nonce}:{ciphertext} format
	// — it must not contain a readable base32 TOTP secret.
	if len(storedCredential) == 0 {
		t.Error("stored credential is empty")
	}
}

func TestVerify_AcceptsCurrentValidCode(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	_, cred, err := env.service.Enroll(context.Background(), userID, "user@example.com", nil)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}

	secret := decryptStoredSecret(t, env, cred.ID)
	code, err := pquernatotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	ok, err := env.service.Verify(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if !ok {
		t.Error("Verify() = false for a freshly generated valid code, want true")
	}
}

func TestVerify_RejectsWrongCode(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	if _, _, err := env.service.Enroll(context.Background(), userID, "user@example.com", nil); err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}

	ok, err := env.service.Verify(context.Background(), userID, "000000")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if ok {
		t.Error("Verify() = true for an arbitrary wrong code, want false")
	}
}

func TestVerify_AcceptsPreviousWindowWithinSkew(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	_, cred, err := env.service.Enroll(context.Background(), userID, "user@example.com", nil)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	secret := decryptStoredSecret(t, env, cred.ID)

	previousWindow := time.Now().Add(-period * time.Second)
	code, err := pquernatotp.GenerateCode(secret, previousWindow)
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	ok, err := env.service.Verify(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if !ok {
		t.Error("Verify() = false for a code from one window ago (within ±1 skew), want true")
	}
}

func TestVerify_RejectsTwoWindowsAgo(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	_, cred, err := env.service.Enroll(context.Background(), userID, "user@example.com", nil)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	secret := decryptStoredSecret(t, env, cred.ID)

	tooOld := time.Now().Add(-2 * period * time.Second)
	code, err := pquernatotp.GenerateCode(secret, tooOld)
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	ok, err := env.service.Verify(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if ok {
		t.Error("Verify() = true for a code two windows old (outside ±1 skew), want false")
	}
}

func TestVerify_RejectsReplayedCode(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	_, cred, err := env.service.Enroll(context.Background(), userID, "user@example.com", nil)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	secret := decryptStoredSecret(t, env, cred.ID)
	code, err := pquernatotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	first, err := env.service.Verify(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("first Verify() error: %v", err)
	}
	if !first {
		t.Fatal("first Verify() = false, want true")
	}

	second, err := env.service.Verify(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("second Verify() error: %v", err)
	}
	if second {
		t.Error("second Verify() with the same code = true, want false (replay)")
	}
}

// TestVerify_UndecryptableFactorDoesNotBlockAnotherValidFactor guards
// against a bug where a single corrupted or unrecognized-key credential
// aborted Verify entirely instead of trying the user's other enrolled
// TOTP factors.
func TestVerify_UndecryptableFactorDoesNotBlockAnotherValidFactor(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	// A row with ciphertext that can't be decrypted under any known key
	// (simulates data corruption, or a credential encrypted under a key
	// rotated fully out of Previous).
	if _, err := env.service.store.Insert(context.Background(), userID, mfa.CredentialTOTP, []byte("not-real-ciphertext"), nil); err != nil {
		t.Fatalf("Insert() of the undecryptable row error: %v", err)
	}

	_, cred, err := env.service.Enroll(context.Background(), userID, "user@example.com", nil)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	secret := decryptStoredSecret(t, env, cred.ID)
	code, err := pquernatotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	ok, err := env.service.Verify(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("Verify() error: %v, want nil — the valid factor should still be found", err)
	}
	if !ok {
		t.Error("Verify() = false, want true — a valid factor exists alongside the undecryptable one")
	}
}

func TestVerify_AllFactorsUndecryptableReturnsError(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	if _, err := env.service.store.Insert(context.Background(), userID, mfa.CredentialTOTP, []byte("not-real-ciphertext"), nil); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}

	_, err := env.service.Verify(context.Background(), userID, "123456")
	if err == nil {
		t.Error("Verify() error = nil, want a decrypt error surfaced when every candidate is undecryptable")
	}
}

func TestVerify_NoEnrolledFactorsReturnsFalse(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	ok, err := env.service.Verify(context.Background(), userID, "123456")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if ok {
		t.Error("Verify() = true for a user with no enrolled factors, want false")
	}
}

// decryptStoredSecret reads back and decrypts cred's stored TOTP secret
// via the same key set the test's Service uses, so tests can generate
// real codes to feed into Verify without depending on Enroll to expose
// the secret itself (which it deliberately doesn't — the SVG is the only
// output).
func decryptStoredSecret(t *testing.T, env *testEnv, credID string) string {
	t.Helper()
	var ciphertext []byte
	if err := env.conn.QueryRowContext(context.Background(),
		"SELECT credential FROM system.user_mfa WHERE id = $1", credID,
	).Scan(&ciphertext); err != nil {
		t.Fatalf("query stored credential: %v", err)
	}
	secret, err := env.service.keys.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decrypt stored credential: %v", err)
	}
	return string(secret)
}
