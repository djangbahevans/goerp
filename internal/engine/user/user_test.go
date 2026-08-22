package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/tenant's tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := NewStore(conn)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store, conn
}

// uniqueEmail keeps each test's rows from colliding with a previous run's
// leftovers or a concurrently-running test — same reasoning as
// internal/engine/tenant's uniqueSlug.
func uniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("t%d@example.com", time.Now().UnixNano())
}

func deleteUser(t *testing.T, conn *sql.DB, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.users WHERE id = $1", id)
	})
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _ := openTestStore(t)

	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

// TestBootstrap_ConcurrentCallsAllSucceed guards against goerp#171 — see
// schema.TestBootstrap_ConcurrentCallsAllSucceed's doc comment for what
// this does and doesn't prove.
func TestBootstrap_ConcurrentCallsAllSucceed(t *testing.T) {
	store, _ := openTestStore(t)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- store.Bootstrap(context.Background())
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Bootstrap() error: %v", err)
		}
	}
}

func TestFindOrCreateInvited_CreatesInvitedUserWithNoPassword(t *testing.T) {
	store, conn := openTestStore(t)
	email := uniqueEmail(t)

	id, err := store.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	deleteUser(t, conn, id)

	got, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.Status != StatusInvited {
		t.Errorf("Status = %q, want %q", got.Status, StatusInvited)
	}
	if got.PasswordHash != nil {
		t.Errorf("PasswordHash = %v, want nil", got.PasswordHash)
	}
	if got.Email != email {
		t.Errorf("Email = %q, want %q", got.Email, email)
	}
}

func TestFindOrCreateInvited_ReusesExistingRow(t *testing.T) {
	store, conn := openTestStore(t)
	email := uniqueEmail(t)

	id1, err := store.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("first FindOrCreateInvited() error: %v", err)
	}
	deleteUser(t, conn, id1)

	id2, err := store.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("second FindOrCreateInvited() error: %v", err)
	}

	if id1 != id2 {
		t.Errorf("second call returned a different id: %q != %q", id1, id2)
	}
}

func TestFindOrCreateInvited_DoesNotTouchAnExistingActiveUser(t *testing.T) {
	store, conn := openTestStore(t)
	email := uniqueEmail(t)

	id, err := store.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	deleteUser(t, conn, id)

	if _, err := conn.ExecContext(context.Background(),
		"UPDATE system.users SET status = 'active', password_hash = 'hash' WHERE id = $1", id,
	); err != nil {
		t.Fatalf("mark user active: %v", err)
	}

	gotID, err := store.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("second FindOrCreateInvited() error: %v", err)
	}
	if gotID != id {
		t.Fatalf("got a different id: %q != %q", gotID, id)
	}

	got, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.Status != StatusActive {
		t.Errorf("Status = %q, want %q (should be untouched)", got.Status, StatusActive)
	}
	if got.PasswordHash == nil || *got.PasswordHash != "hash" {
		t.Errorf("PasswordHash = %v, want it untouched", got.PasswordHash)
	}
}

func TestFindOrCreateInvited_ConcurrentCallsResolveToOneRow(t *testing.T) {
	store, conn := openTestStore(t)
	email := uniqueEmail(t)

	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = store.FindOrCreateInvited(context.Background(), email)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d error: %v", i, err)
		}
	}
	deleteUser(t, conn, ids[0])

	for i, id := range ids {
		if id != ids[0] {
			t.Errorf("call %d returned id %q, want %q (same row as call 0)", i, id, ids[0])
		}
	}
}

func TestGetByEmail_NormalisesCase(t *testing.T) {
	store, conn := openTestStore(t)
	email := uniqueEmail(t)

	id, err := store.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	deleteUser(t, conn, id)

	got, err := store.GetByEmail(context.Background(), "MixedCase"+email)
	if err == nil || got != nil {
		t.Fatalf("lookup with a different email unexpectedly succeeded")
	}

	got, err = store.GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("GetByEmail() error: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
}

func TestGetByEmail_NotFoundReturnsErrUserNotFound(t *testing.T) {
	store, _ := openTestStore(t)

	_, err := store.GetByEmail(context.Background(), uniqueEmail(t))
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("GetByEmail() error = %v, want ErrUserNotFound", err)
	}
}

func TestGetByID_NotFoundReturnsErrUserNotFound(t *testing.T) {
	store, _ := openTestStore(t)

	_, err := store.GetByID(context.Background(), "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("GetByID() error = %v, want ErrUserNotFound", err)
	}
}

func TestGetByEmail_DeletedUserNotFound(t *testing.T) {
	store, conn := openTestStore(t)
	email := uniqueEmail(t)

	id, err := store.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	deleteUser(t, conn, id)

	if _, err := conn.ExecContext(context.Background(),
		"UPDATE system.users SET deleted_at = NOW() WHERE id = $1", id,
	); err != nil {
		t.Fatalf("soft-delete user: %v", err)
	}

	_, err = store.GetByEmail(context.Background(), email)
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("GetByEmail() for a deleted user: error = %v, want ErrUserNotFound", err)
	}

	// Re-inviting the same email after the only existing row is deleted
	// creates a brand-new row, not a reactivation — matches
	// auth-internals.md §2's "User status lifecycle" note.
	newID, err := store.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() after delete: %v", err)
	}
	deleteUser(t, conn, newID)
	if newID == id {
		t.Error("expected a new row for a re-invited, previously-deleted email")
	}
}

func TestIncrementFailedLogins_IncrementsCounter(t *testing.T) {
	store, conn := openTestStore(t)
	id, err := store.FindOrCreateInvited(context.Background(), uniqueEmail(t))
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	defer deleteUser(t, conn, id)

	if err := store.IncrementFailedLogins(context.Background(), id); err != nil {
		t.Fatalf("IncrementFailedLogins() error: %v", err)
	}

	got, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.FailedLoginCount != 1 {
		t.Errorf("FailedLoginCount = %d, want 1", got.FailedLoginCount)
	}
	if got.LockedUntil != nil {
		t.Errorf("LockedUntil = %v, want nil below the threshold", got.LockedUntil)
	}
}

func TestIncrementFailedLogins_LocksAtThreshold(t *testing.T) {
	store, conn := openTestStore(t)
	id, err := store.FindOrCreateInvited(context.Background(), uniqueEmail(t))
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	defer deleteUser(t, conn, id)

	for range failedLoginLockThreshold {
		if err := store.IncrementFailedLogins(context.Background(), id); err != nil {
			t.Fatalf("IncrementFailedLogins() error: %v", err)
		}
	}

	got, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.FailedLoginCount != failedLoginLockThreshold {
		t.Errorf("FailedLoginCount = %d, want %d", got.FailedLoginCount, failedLoginLockThreshold)
	}
	if got.LockedUntil == nil {
		t.Fatal("LockedUntil is nil, want set at the threshold")
	}
	if !got.LockedUntil.After(time.Now()) {
		t.Errorf("LockedUntil = %v, want a future timestamp", got.LockedUntil)
	}
}

func TestResetLoginState_ClearsCounterAndLockAndSetsLastLogin(t *testing.T) {
	store, conn := openTestStore(t)
	id, err := store.FindOrCreateInvited(context.Background(), uniqueEmail(t))
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	defer deleteUser(t, conn, id)

	for range failedLoginLockThreshold {
		if err := store.IncrementFailedLogins(context.Background(), id); err != nil {
			t.Fatalf("IncrementFailedLogins() error: %v", err)
		}
	}

	if err := store.ResetLoginState(context.Background(), id, "203.0.113.5"); err != nil {
		t.Fatalf("ResetLoginState() error: %v", err)
	}

	got, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.FailedLoginCount != 0 {
		t.Errorf("FailedLoginCount = %d, want 0", got.FailedLoginCount)
	}
	if got.LockedUntil != nil {
		t.Errorf("LockedUntil = %v, want nil", got.LockedUntil)
	}

	var lastLoginAt sql.NullTime
	var lastLoginIP sql.NullString
	if err := conn.QueryRowContext(context.Background(),
		"SELECT last_login_at, last_login_ip FROM system.users WHERE id = $1", id,
	).Scan(&lastLoginAt, &lastLoginIP); err != nil {
		t.Fatalf("query last_login fields: %v", err)
	}
	if !lastLoginAt.Valid {
		t.Error("last_login_at is NULL, want set")
	}
	if lastLoginIP.String != "203.0.113.5" {
		t.Errorf("last_login_ip = %q, want %q", lastLoginIP.String, "203.0.113.5")
	}
}

func TestUpdatePasswordHash_OverwritesHash(t *testing.T) {
	store, conn := openTestStore(t)
	id, err := store.FindOrCreateInvited(context.Background(), uniqueEmail(t))
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	defer deleteUser(t, conn, id)

	if err := store.UpdatePasswordHash(context.Background(), id, "new-hash-value"); err != nil {
		t.Fatalf("UpdatePasswordHash() error: %v", err)
	}

	var hash sql.NullString
	if err := conn.QueryRowContext(context.Background(),
		"SELECT password_hash FROM system.users WHERE id = $1", id,
	).Scan(&hash); err != nil {
		t.Fatalf("query password_hash: %v", err)
	}
	if hash.String != "new-hash-value" {
		t.Errorf("password_hash = %q, want %q", hash.String, "new-hash-value")
	}
}
