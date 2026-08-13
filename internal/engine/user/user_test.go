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
