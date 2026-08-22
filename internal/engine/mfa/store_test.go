package mfa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// testEnv wires an mfa.Store plus user.Store against the real
// compose.dev.yml Postgres — user_mfa FK-references users, so tests need
// a real user row.
type testEnv struct {
	store     *Store
	userStore *user.Store
	conn      *sql.DB
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

	store := NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return &testEnv{store: store, userStore: userStore, conn: conn}
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("mfatest%d@example.com", time.Now().UnixNano())
}

// createUser creates a user, fails the test on error, and registers its
// scoped cleanup — user_mfa cascades on delete, so no separate cleanup is
// needed for credential rows.
func (e *testEnv) createUser(t *testing.T) string {
	t.Helper()
	email := uniqueEmail(t)
	userID, err := e.userStore.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited(%q) error: %v", email, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.users WHERE id = $1", userID) })
	return userID
}

func TestBootstrap_CreatesTableAndIndex(t *testing.T) {
	env := openTestEnv(t)

	var tableExists bool
	err := env.conn.QueryRowContext(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'system' AND table_name = 'user_mfa'
		)
	`).Scan(&tableExists)
	if err != nil {
		t.Fatalf("check table exists: %v", err)
	}
	if !tableExists {
		t.Error("expected system.user_mfa to exist after Bootstrap()")
	}

	var indexDef string
	err = env.conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_user_mfa_user_id'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_user_mfa_user_id to exist: %v", err)
	}
	if indexDef == "" {
		t.Fatal("expected a non-empty index definition")
	}
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	env := openTestEnv(t)

	if err := env.store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

// TestBootstrap_ConcurrentCallsAllSucceed guards against goerp#171 — see
// tenant.TestBootstrap_ConcurrentCallsAllSucceed's doc comment for what
// this does and doesn't prove.
func TestBootstrap_ConcurrentCallsAllSucceed(t *testing.T) {
	env := openTestEnv(t)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- env.store.Bootstrap(context.Background())
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

func TestInsert_StoresCredentialAndReturnsRow(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)
	label := "iPhone"

	c, err := env.store.Insert(context.Background(), userID, CredentialTOTP, []byte("ciphertext"), &label)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	if c.ID == "" {
		t.Error("ID = \"\", want a generated id")
	}
	if c.UserID != userID {
		t.Errorf("UserID = %q, want %q", c.UserID, userID)
	}
	if c.Type != CredentialTOTP {
		t.Errorf("Type = %q, want %q", c.Type, CredentialTOTP)
	}
	if string(c.Credential) != "ciphertext" {
		t.Errorf("Credential = %q, want %q", c.Credential, "ciphertext")
	}
	if c.Label == nil || *c.Label != label {
		t.Errorf("Label = %v, want %q", c.Label, label)
	}
	if c.IsPrimary {
		t.Error("IsPrimary = true, want false by default")
	}
	if c.RevokedAt != nil {
		t.Errorf("RevokedAt = %v, want nil for a freshly inserted row", c.RevokedAt)
	}
}

func TestInsert_UnknownUserFails(t *testing.T) {
	env := openTestEnv(t)

	_, err := env.store.Insert(context.Background(), "00000000-0000-0000-0000-000000000000", CredentialTOTP, []byte("x"), nil)
	if err == nil {
		t.Fatal("expected a foreign key violation for an unknown user")
	}
}

func TestListActiveByUser_ReturnsOnlyNonRevoked(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	active, err := env.store.Insert(context.Background(), userID, CredentialTOTP, []byte("active"), nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	revoked, err := env.store.Insert(context.Background(), userID, CredentialRecoveryCode, []byte("revoked"), nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	if err := env.store.Revoke(context.Background(), revoked.ID); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	creds, err := env.store.ListActiveByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListActiveByUser() error: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("ListActiveByUser() returned %d credentials, want 1", len(creds))
	}
	if creds[0].ID != active.ID {
		t.Errorf("ListActiveByUser()[0].ID = %q, want %q", creds[0].ID, active.ID)
	}
}

func TestListActiveByUser_NoCredentialsReturnsEmpty(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	creds, err := env.store.ListActiveByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListActiveByUser() error: %v", err)
	}
	if len(creds) != 0 {
		t.Errorf("ListActiveByUser() = %v, want empty", creds)
	}
}

func TestRevoke_SetsRevokedAt(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	c, err := env.store.Insert(context.Background(), userID, CredentialWebAuthn, []byte("x"), nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	if err := env.store.Revoke(context.Background(), c.ID); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	var revokedAt sql.NullTime
	if err := env.conn.QueryRowContext(context.Background(), "SELECT revoked_at FROM system.user_mfa WHERE id = $1", c.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("query revoked row: %v", err)
	}
	if !revokedAt.Valid {
		t.Error("revoked_at is NULL, want set")
	}
}

func TestRevoke_UnknownIDReturnsErrCredentialNotFound(t *testing.T) {
	env := openTestEnv(t)

	err := env.store.Revoke(context.Background(), "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Errorf("Revoke() error = %v, want ErrCredentialNotFound", err)
	}
}

func TestRevoke_AlreadyRevokedStillSucceeds(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	c, err := env.store.Insert(context.Background(), userID, CredentialTOTP, []byte("x"), nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	if err := env.store.Revoke(context.Background(), c.ID); err != nil {
		t.Fatalf("first Revoke() error: %v", err)
	}

	// Matches session.Store.Revoke/apikey.Store.Revoke: re-revoking an
	// already-revoked id still succeeds (id still matches, revoked_at is
	// just re-set) rather than reporting ErrCredentialNotFound.
	if err := env.store.Revoke(context.Background(), c.ID); err != nil {
		t.Errorf("second Revoke() error = %v, want nil (idempotent)", err)
	}
}

func TestConsumeOnce_SetsRevokedAt(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	c, err := env.store.Insert(context.Background(), userID, CredentialRecoveryCode, []byte("x"), nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	if err := env.store.ConsumeOnce(context.Background(), c.ID); err != nil {
		t.Fatalf("ConsumeOnce() error: %v", err)
	}

	var revokedAt sql.NullTime
	if err := env.conn.QueryRowContext(context.Background(), "SELECT revoked_at FROM system.user_mfa WHERE id = $1", c.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("query revoked row: %v", err)
	}
	if !revokedAt.Valid {
		t.Error("revoked_at is NULL, want set")
	}
}

func TestConsumeOnce_UnknownIDReturnsErrCredentialNotFound(t *testing.T) {
	env := openTestEnv(t)

	err := env.store.ConsumeOnce(context.Background(), "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Errorf("ConsumeOnce() error = %v, want ErrCredentialNotFound", err)
	}
}

func TestConsumeOnce_AlreadyConsumedReturnsErrCredentialNotFound(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	c, err := env.store.Insert(context.Background(), userID, CredentialRecoveryCode, []byte("x"), nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	if err := env.store.ConsumeOnce(context.Background(), c.ID); err != nil {
		t.Fatalf("first ConsumeOnce() error: %v", err)
	}

	// Unlike Revoke, a second ConsumeOnce on the same id must fail — this
	// is the whole point of the method for a single-use token.
	err = env.store.ConsumeOnce(context.Background(), c.ID)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Errorf("second ConsumeOnce() error = %v, want ErrCredentialNotFound", err)
	}
}

// TestConsumeOnce_ConcurrentCallsOnlyOneSucceeds guards the exact property
// ConsumeOnce exists for: two callers racing to consume the same
// single-use credential must not both report success.
func TestConsumeOnce_ConcurrentCallsOnlyOneSucceeds(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t)

	c, err := env.store.Insert(context.Background(), userID, CredentialRecoveryCode, []byte("x"), nil)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 10)
	for range 10 {
		wg.Go(func() {
			results <- env.store.ConsumeOnce(context.Background(), c.ID)
		})
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrCredentialNotFound) {
			t.Errorf("ConsumeOnce() error = %v, want nil or ErrCredentialNotFound", err)
		}
	}
	if successes != 1 {
		t.Errorf("successes = %d across 10 concurrent ConsumeOnce() calls, want exactly 1", successes)
	}
}
