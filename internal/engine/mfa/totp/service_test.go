package totp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// seedFactor stores an active TOTP factor for userID directly, returning
// its row id and plaintext secret.
func seedFactor(t *testing.T, env *testEnv, userID string) (credID, secret string) {
	t.Helper()
	key, err := pquernatotp.Generate(pquernatotp.GenerateOpts{Issuer: issuer, AccountName: "user@example.com"})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	ciphertext, err := env.service.keys.Encrypt([]byte(key.Secret()))
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}
	cred, err := env.service.store.Insert(context.Background(), userID, mfa.CredentialTOTP, ciphertext, nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	return cred.ID, key.Secret()
}

func activeFactorCount(t *testing.T, env *testEnv, userID string) int {
	t.Helper()
	creds, err := env.service.store.ListActiveByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListActiveByUser() error: %v", err)
	}
	return len(creds)
}

func TestBeginEnrollment_ReturnsSecretAndSVGWithoutStoringAFactor(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	pending, err := env.service.BeginEnrollment(context.Background(), userID, "user@example.com")
	if err != nil {
		t.Fatalf("BeginEnrollment() error: %v", err)
	}
	if pending.ID == "" || pending.Secret == "" || !strings.HasPrefix(string(pending.QRSVG), "<svg") {
		t.Errorf("BeginEnrollment() = %+v, want an id, a secret, and an SVG", pending)
	}
	if n := activeFactorCount(t, env, userID); n != 0 {
		t.Errorf("active factors after BeginEnrollment = %d, want 0", n)
	}
}

func TestCheckEnrollmentCode_ReturnsSecretAndLeavesEnrollmentUntilClaimed(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	ctx := context.Background()

	pending, err := env.service.BeginEnrollment(ctx, userID, "user@example.com")
	if err != nil {
		t.Fatalf("BeginEnrollment() error: %v", err)
	}
	code, err := pquernatotp.GenerateCode(pending.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	verified, err := env.service.CheckEnrollmentCode(ctx, userID, pending.ID, code)
	if err != nil {
		t.Fatalf("CheckEnrollmentCode() error: %v", err)
	}
	plain, err := env.service.keys.Decrypt(verified.Secret)
	if err != nil {
		t.Fatalf("Decrypt() error: %v", err)
	}
	if string(plain) != pending.Secret {
		t.Error("CheckEnrollmentCode() secret doesn't decrypt to the pending secret")
	}
	if _, found, err := env.service.cache.Get(ctx, enrollmentKey(pending.ID)); err != nil || !found {
		t.Errorf("pending enrollment after a successful check: found %v, %v; want still pending", found, err)
	}

	if err := env.service.ClaimEnrollment(ctx, verified); err != nil {
		t.Fatalf("ClaimEnrollment() error: %v", err)
	}
	if err := env.service.ClaimEnrollment(ctx, verified); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Errorf("second ClaimEnrollment() error = %v, want ErrEnrollmentNotFound", err)
	}
	next, err := pquernatotp.GenerateCode(pending.Secret, time.Now().Add(-period*time.Second))
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}
	if _, err := env.service.CheckEnrollmentCode(ctx, userID, pending.ID, next); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Errorf("CheckEnrollmentCode() after claim error = %v, want ErrEnrollmentNotFound", err)
	}
}

func TestCheckEnrollmentCode_WrongLengthCodeIsInvalidNotAnError(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	ctx := context.Background()

	pending, err := env.service.BeginEnrollment(ctx, userID, "user@example.com")
	if err != nil {
		t.Fatalf("BeginEnrollment() error: %v", err)
	}
	if _, err := env.service.CheckEnrollmentCode(ctx, userID, pending.ID, "12345"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("CheckEnrollmentCode() with a 5-digit code error = %v, want ErrInvalidCode", err)
	}

	seedFactor(t, env, userID)
	if ok, _, err := env.service.Verify(ctx, userID, "1234567"); err != nil || ok {
		t.Errorf("Verify() with a 7-digit code = %v, %v; want false, nil", ok, err)
	}
}

func TestCheckEnrollmentCode_RejectsAnotherUsersEnrollment(t *testing.T) {
	env := openTestEnv(t)
	owner := env.createUser(t)
	other := env.createUser(t)
	ctx := context.Background()

	pending, err := env.service.BeginEnrollment(ctx, owner, "owner@example.com")
	if err != nil {
		t.Fatalf("BeginEnrollment() error: %v", err)
	}
	code, err := pquernatotp.GenerateCode(pending.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	if _, err := env.service.CheckEnrollmentCode(ctx, other, pending.ID, code); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Errorf("CheckEnrollmentCode() by another user error = %v, want ErrEnrollmentNotFound", err)
	}
	if _, err := env.service.CheckEnrollmentCode(ctx, owner, pending.ID, code); err != nil {
		t.Errorf("CheckEnrollmentCode() by the owner afterwards error = %v, want nil", err)
	}
}

func TestCheckEnrollmentCode_FifthWrongCodeDiscardsEnrollment(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	ctx := context.Background()

	pending, err := env.service.BeginEnrollment(ctx, userID, "user@example.com")
	if err != nil {
		t.Fatalf("BeginEnrollment() error: %v", err)
	}
	valid, err := pquernatotp.GenerateCode(pending.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}
	wrong := "000000"
	if wrong == valid {
		wrong = "111111"
	}

	for i := range maxConfirmAttempts {
		if _, err := env.service.CheckEnrollmentCode(ctx, userID, pending.ID, wrong); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("wrong attempt %d error = %v, want ErrInvalidCode", i+1, err)
		}
	}
	if _, err := env.service.CheckEnrollmentCode(ctx, userID, pending.ID, valid); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Errorf("CheckEnrollmentCode() after %d wrong codes error = %v, want ErrEnrollmentNotFound", maxConfirmAttempts, err)
	}
}

func TestVerify_AcceptsCurrentValidCode(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	_, secret := seedFactor(t, env, userID)
	code, err := pquernatotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	ok, _, err := env.service.Verify(context.Background(), userID, code)
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

	seedFactor(t, env, userID)

	ok, _, err := env.service.Verify(context.Background(), userID, "000000")
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

	_, secret := seedFactor(t, env, userID)

	previousWindow := time.Now().Add(-period * time.Second)
	code, err := pquernatotp.GenerateCode(secret, previousWindow)
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	ok, _, err := env.service.Verify(context.Background(), userID, code)
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

	_, secret := seedFactor(t, env, userID)

	tooOld := time.Now().Add(-2 * period * time.Second)
	code, err := pquernatotp.GenerateCode(secret, tooOld)
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	ok, _, err := env.service.Verify(context.Background(), userID, code)
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

	_, secret := seedFactor(t, env, userID)
	code, err := pquernatotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	first, _, err := env.service.Verify(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("first Verify() error: %v", err)
	}
	if !first {
		t.Fatal("first Verify() = false, want true")
	}

	second, _, err := env.service.Verify(context.Background(), userID, code)
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

	_, secret := seedFactor(t, env, userID)
	code, err := pquernatotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}

	ok, _, err := env.service.Verify(context.Background(), userID, code)
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

	_, _, err := env.service.Verify(context.Background(), userID, "123456")
	if err == nil {
		t.Error("Verify() error = nil, want a decrypt error surfaced when every candidate is undecryptable")
	}
}

func TestVerify_NoEnrolledFactorsReturnsFalse(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	ok, _, err := env.service.Verify(context.Background(), userID, "123456")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if ok {
		t.Error("Verify() = true for a user with no enrolled factors, want false")
	}
}
