package recoverycode

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

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

	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}

	mfaStore := mfa.NewStore(conn)
	if err := mfaStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
	}

	return &testEnv{
		service: NewService(mfaStore),
		conn:    conn,
		users:   userStore,
	}
}

func (e *testEnv) createUser(t *testing.T) string {
	t.Helper()
	email := fmt.Sprintf("recoverycodetest%d@example.com", time.Now().UnixNano())
	userID, err := e.users.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited(%q) error: %v", email, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.users WHERE id = $1", userID) })
	return userID
}

// insertCode hashes and stores exactly one recovery-code row directly —
// bypassing Enroll's full 10-code generation — for tests that only need
// one known code to exercise Verify. Bcrypt at the real cost 12 makes a
// full Enroll (10 sequential hashes) too slow to repeat in every test;
// TestEnroll_StoresTenBcryptHashedRows is the one test that exercises the
// real Enroll path end to end.
func (e *testEnv) insertCode(t *testing.T, userID, code string) *mfa.Credential {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcryptCost)
	if err != nil {
		t.Fatalf("hash code: %v", err)
	}
	cred, err := e.service.store.Insert(context.Background(), userID, mfa.CredentialRecoveryCode, hash, nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	return cred
}

func TestEnroll_StoresTenBcryptHashedRows(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	codes, err := env.service.Enroll(context.Background(), userID)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	if len(codes) != codeCount {
		t.Fatalf("len(codes) = %d, want %d", len(codes), codeCount)
	}

	var rowCount int
	if err := env.conn.QueryRowContext(context.Background(),
		"SELECT count(*) FROM system.user_mfa WHERE user_id = $1 AND type = 'recovery_code'", userID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != codeCount {
		t.Errorf("stored row count = %d, want %d", rowCount, codeCount)
	}

	var credential []byte
	if err := env.conn.QueryRowContext(context.Background(),
		"SELECT credential FROM system.user_mfa WHERE user_id = $1 AND type = 'recovery_code' LIMIT 1", userID,
	).Scan(&credential); err != nil {
		t.Fatalf("query credential: %v", err)
	}
	// Stored bcrypt hashes must not be recoverable to the plaintext code
	// by inspection — a bcrypt hash starts with "$2".
	if len(credential) == 0 || credential[0] != '$' {
		t.Errorf("stored credential = %q, want a bcrypt hash (starts with $)", credential)
	}
}

func TestVerify_AcceptsAnEnrolledCode(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	env.insertCode(t, userID, "AAAAA-AAAAA")

	ok, err := env.service.Verify(context.Background(), userID, "AAAAA-AAAAA")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if !ok {
		t.Error("Verify() = false for a freshly enrolled code, want true")
	}
}

func TestVerify_RejectsWrongCode(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	env.insertCode(t, userID, "AAAAA-AAAAA")

	ok, err := env.service.Verify(context.Background(), userID, "BBBBB-BBBBB")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if ok {
		t.Error("Verify() = true for an arbitrary wrong code, want false")
	}
}

func TestVerify_ConsumesTheCodeSoItCannotBeReused(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	env.insertCode(t, userID, "AAAAA-AAAAA")

	first, err := env.service.Verify(context.Background(), userID, "AAAAA-AAAAA")
	if err != nil {
		t.Fatalf("first Verify() error: %v", err)
	}
	if !first {
		t.Fatal("first Verify() = false, want true")
	}

	second, err := env.service.Verify(context.Background(), userID, "AAAAA-AAAAA")
	if err != nil {
		t.Fatalf("second Verify() error: %v", err)
	}
	if second {
		t.Error("second Verify() with the same code = true, want false (already consumed)")
	}
}

func TestVerify_ConsumingOneCodeDoesNotAffectOthers(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	env.insertCode(t, userID, "AAAAA-AAAAA")
	env.insertCode(t, userID, "BBBBB-BBBBB")

	if ok, err := env.service.Verify(context.Background(), userID, "AAAAA-AAAAA"); err != nil || !ok {
		t.Fatalf("Verify(AAAAA-AAAAA) = %v, %v, want true, nil", ok, err)
	}

	ok, err := env.service.Verify(context.Background(), userID, "BBBBB-BBBBB")
	if err != nil {
		t.Fatalf("Verify(BBBBB-BBBBB) error: %v", err)
	}
	if !ok {
		t.Error("Verify(BBBBB-BBBBB) = false after consuming a different code, want true — each code is independent")
	}
}

// TestVerify_ConcurrentCallsWithSameCodeOnlyOneSucceeds guards the same
// exactly-once property mfa.Store.ConsumeOnce's own concurrency test
// covers, at this package's level.
func TestVerify_ConcurrentCallsWithSameCodeOnlyOneSucceeds(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	env.insertCode(t, userID, "AAAAA-AAAAA")

	var wg sync.WaitGroup
	results := make(chan bool, 10)
	for range 10 {
		wg.Go(func() {
			ok, _ := env.service.Verify(context.Background(), userID, "AAAAA-AAAAA")
			results <- ok
		})
	}
	wg.Wait()
	close(results)

	successes := 0
	for ok := range results {
		if ok {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("successes = %d across 10 concurrent Verify() calls with the same code, want exactly 1", successes)
	}
}

func TestVerify_NoEnrolledCodesReturnsFalse(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	ok, err := env.service.Verify(context.Background(), userID, "AAAAA-AAAAA")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if ok {
		t.Error("Verify() = true for a user with no enrolled codes, want false")
	}
}
