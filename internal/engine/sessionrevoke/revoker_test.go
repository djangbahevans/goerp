package sessionrevoke

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/session"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/google/uuid"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fixture is a Revoker plus one real session row (and its FK-satisfying
// tenant/user rows), cleaned up by exact row id.
type fixture struct {
	revoker   *Revoker
	sessionID string
	userID    string
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

	sessionID := uuid.NewString()
	if err := sessionStore.Insert(ctx, session.Row{
		ID: sessionID, UserID: userID, TenantID: tt.ID, DeviceID: uuid.NewString(),
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
