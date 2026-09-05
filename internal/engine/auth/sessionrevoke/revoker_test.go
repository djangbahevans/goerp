package sessionrevoke

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fixture is a Revoker plus one real session row (and its FK-satisfying
// tenant/user rows), cleaned up by exact row id.
type fixture struct {
	revoker   *Revoker
	sessionID string
	userID    string
	tenantID  string
	conn      *sql.DB
	cache     *cache.Client
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	sessionStore := session.NewStore(conn)
	if err := sessionStore.Bootstrap(ctx); err != nil {
		t.Fatalf("session Bootstrap() error: %v", err)
	}
	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("revoketest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Revoke Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	userID, err := userStore.FindOrCreateInvited(ctx, slug+"@example.com")
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })

	sessionID := uuid.New().String()
	if err := sessionStore.Insert(ctx, session.Row{
		ID: sessionID, UserID: userID, TenantID: tt.ID, DeviceID: uuid.New().String(),
		RefreshHash: "fixture-hash", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE id = $1`, sessionID) })
	// Any blocklist key a test sets bounds itself by blocklistTTL — no
	// explicit cleanup needed, and cache.Client has no Del method to call.

	return &fixture{
		revoker:   NewRevoker(sessionStore, cacheClient),
		sessionID: sessionID,
		userID:    userID,
		tenantID:  tt.ID,
		conn:      conn,
		cache:     cacheClient,
	}
}

func TestRevoke_SetsDBRowAndBlocklist(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.revoker.Revoke(ctx, f.sessionID, "logout"); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	var revokedAt sql.NullTime
	if err := f.conn.QueryRowContext(ctx, `SELECT revoked_at FROM system.sessions WHERE id = $1`, f.sessionID).Scan(&revokedAt); err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if !revokedAt.Valid {
		t.Error("revoked_at is NULL, want set")
	}

	blocked, err := f.revoker.IsBlocked(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("IsBlocked() error: %v", err)
	}
	if !blocked {
		t.Error("IsBlocked() = false, want true after Revoke")
	}
}

func TestIsBlocked_FalseForNeverRevokedSession(t *testing.T) {
	f := newFixture(t)

	blocked, err := f.revoker.IsBlocked(context.Background(), f.sessionID)
	if err != nil {
		t.Fatalf("IsBlocked() error: %v", err)
	}
	if blocked {
		t.Error("IsBlocked() = true for a never-revoked session, want false")
	}
}

func TestRevokeAllForUser_BlocklistsEverySession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.revoker.RevokeAllForUser(ctx, f.userID, "admin"); err != nil {
		t.Fatalf("RevokeAllForUser() error: %v", err)
	}

	blocked, err := f.revoker.IsBlocked(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("IsBlocked() error: %v", err)
	}
	if !blocked {
		t.Error("IsBlocked() = false after RevokeAllForUser, want true")
	}
}

func TestIsRolesStale_FalseWhenNeverMarked(t *testing.T) {
	f := newFixture(t)

	stale, err := f.revoker.IsRolesStale(context.Background(), f.sessionID)
	if err != nil {
		t.Fatalf("IsRolesStale() error: %v", err)
	}
	if stale {
		t.Error("IsRolesStale() = true for a never-marked session, want false")
	}
}

func TestMarkRolesStale_ThenIsRolesStaleReturnsTrue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.revoker.MarkRolesStale(ctx, f.sessionID); err != nil {
		t.Fatalf("MarkRolesStale() error: %v", err)
	}

	stale, err := f.revoker.IsRolesStale(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("IsRolesStale() error: %v", err)
	}
	if !stale {
		t.Error("IsRolesStale() = false after MarkRolesStale, want true")
	}
}

func TestMarkRolesStaleForUserInTenant_OnlyMarksThatTenantsSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A second session for the same user, in a different tenant — must
	// stay untouched by a tenant-scoped mark-stale.
	tenantStore := tenant.NewStore(f.conn)
	slug2 := fmt.Sprintf("markstaletest2%d", time.Now().UnixNano())
	tt2, err := tenantStore.CreateTenant(ctx, slug2, "Mark Stale Test Co 2")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt2.ID) })

	otherSessionID := uuid.New().String()
	sessionStore := session.NewStore(f.conn)
	if err := sessionStore.Insert(ctx, session.Row{
		ID: otherSessionID, UserID: f.userID, TenantID: tt2.ID, DeviceID: uuid.New().String(),
		RefreshHash: "fixture-hash-other-tenant", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("Insert() second session error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE id = $1`, otherSessionID) })

	if err := f.revoker.MarkRolesStaleForUserInTenant(ctx, f.userID, f.tenantID); err != nil {
		t.Fatalf("MarkRolesStaleForUserInTenant() error: %v", err)
	}

	stale, err := f.revoker.IsRolesStale(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("IsRolesStale() fixture session error: %v", err)
	}
	if !stale {
		t.Error("IsRolesStale() = false for the fixture tenant's session, want true")
	}

	otherStale, err := f.revoker.IsRolesStale(ctx, otherSessionID)
	if err != nil {
		t.Fatalf("IsRolesStale() other-tenant session error: %v", err)
	}
	if otherStale {
		t.Error("IsRolesStale() = true for the other tenant's session, want false — must be tenant-scoped")
	}
}

func TestRevokeAllForUserInTenant_OnlyBlocklistsThatTenantsSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A second session for the same user, in a different tenant — must
	// stay untouched by a tenant-scoped revoke.
	tenantStore := tenant.NewStore(f.conn)
	slug2 := fmt.Sprintf("revoketest2%d", time.Now().UnixNano())
	tt2, err := tenantStore.CreateTenant(ctx, slug2, "Revoke Test Co 2")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt2.ID) })

	otherSessionID := uuid.New().String()
	sessionStore := session.NewStore(f.conn)
	if err := sessionStore.Insert(ctx, session.Row{
		ID: otherSessionID, UserID: f.userID, TenantID: tt2.ID, DeviceID: uuid.New().String(),
		RefreshHash: "fixture-hash-other-tenant", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("Insert() second session error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE id = $1`, otherSessionID) })

	if err := f.revoker.RevokeAllForUserInTenant(ctx, f.userID, f.tenantID, "admin_mfa_reset"); err != nil {
		t.Fatalf("RevokeAllForUserInTenant() error: %v", err)
	}

	blocked, err := f.revoker.IsBlocked(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("IsBlocked() fixture session error: %v", err)
	}
	if !blocked {
		t.Error("IsBlocked() = false for the fixture tenant's session, want true")
	}

	otherBlocked, err := f.revoker.IsBlocked(ctx, otherSessionID)
	if err != nil {
		t.Fatalf("IsBlocked() other-tenant session error: %v", err)
	}
	if otherBlocked {
		t.Error("IsBlocked() = true for the other tenant's session, want false — must be tenant-scoped")
	}
}

func TestRevokeAllForTenant_BlocklistsEverySession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.revoker.RevokeAllForTenant(ctx, f.tenantID, "tenant_suspended"); err != nil {
		t.Fatalf("RevokeAllForTenant() error: %v", err)
	}

	var revokedAt sql.NullTime
	if err := f.conn.QueryRowContext(ctx, `SELECT revoked_at FROM system.sessions WHERE id = $1`, f.sessionID).Scan(&revokedAt); err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if !revokedAt.Valid {
		t.Error("revoked_at is NULL, want set")
	}

	blocked, err := f.revoker.IsBlocked(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("IsBlocked() error: %v", err)
	}
	if !blocked {
		t.Error("IsBlocked() = false after RevokeAllForTenant, want true")
	}
}
