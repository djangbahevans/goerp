package invite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fakeUserResolver hands out a fresh fake id per unique email, without
// needing a real system.users table — most tests here exercise
// invite.Store's own behavior, not user.Store's.
type fakeUserResolver struct {
	ids map[string]string
	n   int
}

func newFakeUserResolver() *fakeUserResolver {
	return &fakeUserResolver{ids: make(map[string]string)}
}

func (f *fakeUserResolver) FindOrCreateInvited(ctx context.Context, email string) (string, error) {
	if id, ok := f.ids[email]; ok {
		return id, nil
	}
	f.n++
	id := fmt.Sprintf("fake-user-%d", f.n)
	f.ids[email] = id
	return id, nil
}

// openTestStore creates a fixture tenant_<random> schema with roles
// bootstrapped and seeded (a real admin role to invite against), then a
// Store wired to a fake UserResolver and nil audit/mailer.
func openTestStore(t *testing.T) (store *Store, conn *sql.DB, tenantSlug string) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("invitetest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)

	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	roleStore := role.NewStore(conn)
	if err := roleStore.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roleStore.SeedBuiltinRoles(context.Background(), slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}

	store = NewStore(conn, newFakeUserResolver(), roleStore, nil, nil)
	if err := store.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("invite Bootstrap() error: %v", err)
	}

	return store, conn, slug
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("t%d@example.com", time.Now().UnixNano())
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _, slug := openTestStore(t)

	if err := store.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

// TestBootstrap_ConcurrentCallsAgainstFreshSchemaAllSucceed guards
// against goerp#171 directly against the original failure mode — see
// role's identically-named test for why this needs its own fresh schema
// rather than reusing openTestStore. role.Store.Bootstrap runs once
// (single call, not concurrent) first since tenant_invitations.role_id
// references {schema}.roles(id) — only the tenant_invitations creation
// itself is exercised concurrently here.
func TestBootstrap_ConcurrentCallsAgainstFreshSchemaAllSucceed(t *testing.T) {
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("inviteconcurrent%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	roleStore := role.NewStore(conn)
	if err := roleStore.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}

	store := NewStore(conn, newFakeUserResolver(), roleStore, nil, nil)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- store.Bootstrap(context.Background(), slug)
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

func TestInvite_CreatesInvitationForUnknownEmail(t *testing.T) {
	store, _, slug := openTestStore(t)
	email := uniqueEmail(t)

	inv, err := store.Invite(context.Background(), slug, email, "admin", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}

	if inv.Email != email {
		t.Errorf("Email = %q, want %q", inv.Email, email)
	}
	if inv.AcceptedAt != nil {
		t.Error("expected AcceptedAt to be nil")
	}
	if inv.RevokedAt != nil {
		t.Error("expected RevokedAt to be nil")
	}
	if inv.ExpiresAt.Before(time.Now().Add(6 * 24 * time.Hour)) {
		t.Errorf("ExpiresAt = %v, want roughly 7 days out", inv.ExpiresAt)
	}
}

func TestInvite_UnknownRoleFails(t *testing.T) {
	store, _, slug := openTestStore(t)

	_, err := store.Invite(context.Background(), slug, uniqueEmail(t), "does-not-exist", nil)
	if !errors.Is(err, role.ErrRoleNotFound) {
		t.Errorf("Invite() with unknown role: error = %v, want role.ErrRoleNotFound", err)
	}
}

func TestInvite_ReinvitingLiveEmailReusesRowAndRotatesToken(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	email := uniqueEmail(t)

	first, err := store.Invite(context.Background(), slug, email, "admin", nil)
	if err != nil {
		t.Fatalf("first Invite() error: %v", err)
	}

	var firstHash string
	if err := conn.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT token_hash FROM %s.tenant_invitations WHERE id = $1", schema), first.ID,
	).Scan(&firstHash); err != nil {
		t.Fatalf("query first token_hash: %v", err)
	}

	second, err := store.Invite(context.Background(), slug, email, "admin", nil)
	if err != nil {
		t.Fatalf("second Invite() error: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("second Invite() created a new row: %q != %q", second.ID, first.ID)
	}

	var secondHash string
	if err := conn.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT token_hash FROM %s.tenant_invitations WHERE id = $1", schema), first.ID,
	).Scan(&secondHash); err != nil {
		t.Fatalf("query second token_hash: %v", err)
	}
	if secondHash == firstHash {
		t.Error("expected token_hash to rotate on re-invite")
	}

	var count int
	if err := conn.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT count(*) FROM %s.tenant_invitations WHERE email = $1", schema), email,
	).Scan(&count); err != nil {
		t.Fatalf("count invitations: %v", err)
	}
	if count != 1 {
		t.Errorf("invitation count for email = %d, want 1", count)
	}
}

func TestResend_NonLiveInvitationReturnsErrInvitationNotLive(t *testing.T) {
	store, _, slug := openTestStore(t)

	_, err := store.Resend(context.Background(), slug, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrInvitationNotLive) {
		t.Errorf("Resend() for a nonexistent id: error = %v, want ErrInvitationNotLive", err)
	}
}

func TestRevoke_FreesEmailForFreshInvite(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	email := uniqueEmail(t)

	first, err := store.Invite(context.Background(), slug, email, "admin", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}

	if err := store.Revoke(context.Background(), slug, first.ID); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	var revokedAt sql.NullTime
	if err := conn.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT revoked_at FROM %s.tenant_invitations WHERE id = $1", schema), first.ID,
	).Scan(&revokedAt); err != nil {
		t.Fatalf("query revoked_at: %v", err)
	}
	if !revokedAt.Valid {
		t.Fatal("expected revoked_at to be set")
	}

	// A fresh invite to the same email now creates a NEW row — the
	// revoked one is excluded from the partial index's conflict target.
	second, err := store.Invite(context.Background(), slug, email, "admin", nil)
	if err != nil {
		t.Fatalf("Invite() after revoke: %v", err)
	}
	if second.ID == first.ID {
		t.Error("expected a new invitation row after revoking the previous one")
	}
}

func TestRevoke_NonLiveReturnsErrInvitationNotLive(t *testing.T) {
	store, _, slug := openTestStore(t)
	email := uniqueEmail(t)

	inv, err := store.Invite(context.Background(), slug, email, "admin", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}
	if err := store.Revoke(context.Background(), slug, inv.ID); err != nil {
		t.Fatalf("first Revoke() error: %v", err)
	}

	if err := store.Revoke(context.Background(), slug, inv.ID); !errors.Is(err, ErrInvitationNotLive) {
		t.Errorf("second Revoke() on an already-revoked invitation: error = %v, want ErrInvitationNotLive", err)
	}
}

func TestResendInvite_ResolvesEmailAndRotatesToken(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	email := uniqueEmail(t)

	inv, err := store.Invite(context.Background(), slug, email, "admin", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}

	var before string
	if err := conn.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT token_hash FROM %s.tenant_invitations WHERE id = $1", schema), inv.ID,
	).Scan(&before); err != nil {
		t.Fatalf("query token_hash: %v", err)
	}

	if err := store.ResendInvite(context.Background(), slug, email); err != nil {
		t.Fatalf("ResendInvite() error: %v", err)
	}

	var after string
	if err := conn.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT token_hash FROM %s.tenant_invitations WHERE id = $1", schema), inv.ID,
	).Scan(&after); err != nil {
		t.Fatalf("query token_hash: %v", err)
	}
	if after == before {
		t.Error("expected token_hash to rotate via ResendInvite")
	}
}

func TestResendInvite_UnknownEmailReturnsErrInvitationNotLive(t *testing.T) {
	store, _, slug := openTestStore(t)

	err := store.ResendInvite(context.Background(), slug, uniqueEmail(t))
	if !errors.Is(err, ErrInvitationNotLive) {
		t.Errorf("ResendInvite() for an unknown email: error = %v, want ErrInvitationNotLive", err)
	}
}

// TestInvite_ComposesWithRealUserStore proves invite.Store and
// user.Store actually satisfy each other's interfaces end to end, not
// just against the fake resolver the other tests use.
func TestInvite_ComposesWithRealUserStore(t *testing.T) {
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("invitereal%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	roleStore := role.NewStore(conn)
	if err := roleStore.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roleStore.SeedBuiltinRoles(context.Background(), slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}

	inviteStore := NewStore(conn, userStore, roleStore, nil, nil)
	if err := inviteStore.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("invite Bootstrap() error: %v", err)
	}

	email := uniqueEmail(t)
	inv, err := inviteStore.Invite(context.Background(), slug, email, "admin", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.users WHERE email = $1", email)
	})

	got, err := userStore.GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("GetByEmail() error: %v", err)
	}
	if got.Status != user.StatusInvited {
		t.Errorf("Status = %q, want %q", got.Status, user.StatusInvited)
	}
	_ = inv
}
