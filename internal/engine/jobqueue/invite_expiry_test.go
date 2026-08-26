package jobqueue

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const inviteExpiryTestDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fakeUserResolver hands out a fresh fake id per unique email, mirroring
// invite package's own test fixture — this package can't import invite's
// unexported one.
type fakeUserResolver struct {
	n int
}

func (f *fakeUserResolver) FindOrCreateInvited(ctx context.Context, email string) (string, error) {
	f.n++
	return fmt.Sprintf("fake-user-%d", f.n), nil
}

func openInviteExpiryWorker(t *testing.T) (*InviteExpiryWorker, *tenant.Store, *invite.Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(inviteExpiryTestDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", inviteExpiryTestDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("tenant.Bootstrap() error: %v", err)
	}

	roleStore := role.NewStore(conn)
	inviteStore := invite.NewStore(conn, &fakeUserResolver{}, roleStore, nil, nil)

	authStore := authaudit.NewStore(conn, tenantStore)
	if err := authStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("authaudit.Bootstrap() error: %v", err)
	}

	w := &InviteExpiryWorker{TenantStore: tenantStore, InviteStore: inviteStore, AuditStore: authStore}
	return w, tenantStore, inviteStore, conn
}

func newExpiryTestTenant(t *testing.T, tenantStore *tenant.Store, roleStore *role.Store, inviteStore *invite.Store, conn *sql.DB) *tenant.Tenant {
	t.Helper()
	ctx := context.Background()
	slug := fmt.Sprintf("expirytest%d", time.Now().UnixNano())

	tt, err := tenantStore.CreateTenant(ctx, slug, "Invite Expiry Test")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	if _, err := tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}
	t.Cleanup(func() {
		// auth_audit_log.tenant_id REFERENCES system.tenants(id) with no
		// cascade (deliberate — an audit trail must outlive the tenant it
		// records, auth-internals.md §17) — the tenant row can't be deleted
		// while this test's Emit calls left rows referencing it.
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.auth_audit_log WHERE tenant_id = $1", tt.ID)
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.tenants WHERE id = $1", tt.ID)
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+tenantschema.Name(slug)+" CASCADE")
	})

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+tenantschema.Name(slug)); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	if err := roleStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roleStore.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	if err := inviteStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("invite Bootstrap() error: %v", err)
	}

	return tt
}

func backdateInviteExpiry(t *testing.T, conn *sql.DB, slug, invitationID string, expiresAt time.Time) {
	t.Helper()
	_, err := conn.ExecContext(context.Background(),
		fmt.Sprintf("UPDATE %s.tenant_invitations SET expires_at = $1 WHERE id = $2", tenantschema.Name(slug)),
		expiresAt, invitationID)
	if err != nil {
		t.Fatalf("backdate expires_at: %v", err)
	}
}

func runWork(t *testing.T, w *InviteExpiryWorker) {
	t.Helper()
	if err := w.Work(context.Background(), &river.Job[InviteExpiryArgs]{JobRow: &rivertype.JobRow{}, Args: InviteExpiryArgs{}}); err != nil {
		t.Fatalf("Work() error: %v", err)
	}
}

func TestWork_EmitsExpiredInviteExactlyOnceAcrossRuns(t *testing.T) {
	w, tenantStore, inviteStore, conn := openInviteExpiryWorker(t)
	roleStore := role.NewStore(conn)
	ctx := context.Background()

	tt := newExpiryTestTenant(t, tenantStore, roleStore, inviteStore, conn)

	inv, err := inviteStore.Invite(ctx, tt.Slug, fmt.Sprintf("t%d@example.com", time.Now().UnixNano()), "admin", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}
	backdateInviteExpiry(t, conn, tt.Slug, inv.ID, time.Now().Add(-time.Hour))

	runWork(t, w)
	runWork(t, w)

	var count int
	err = conn.QueryRowContext(ctx,
		`SELECT count(*) FROM system.auth_audit_log WHERE event_type = 'user.invite_expired' AND metadata->>'invitation_id' = $1`,
		inv.ID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count auth_audit_log rows: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 user.invite_expired row after 2 Work() calls, got %d", count)
	}
}

func TestWork_SkipsLiveAcceptedAndRevokedInvitations(t *testing.T) {
	w, tenantStore, inviteStore, conn := openInviteExpiryWorker(t)
	roleStore := role.NewStore(conn)
	ctx := context.Background()

	tt := newExpiryTestTenant(t, tenantStore, roleStore, inviteStore, conn)

	live, err := inviteStore.Invite(ctx, tt.Slug, fmt.Sprintf("live%d@example.com", time.Now().UnixNano()), "admin", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}

	accepted, err := inviteStore.Invite(ctx, tt.Slug, fmt.Sprintf("accepted%d@example.com", time.Now().UnixNano()), "admin", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}
	backdateInviteExpiry(t, conn, tt.Slug, accepted.ID, time.Now().Add(-time.Hour))
	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s.tenant_invitations SET accepted_at = NOW() WHERE id = $1", tenantschema.Name(tt.Slug)),
		accepted.ID,
	); err != nil {
		t.Fatalf("mark accepted: %v", err)
	}

	runWork(t, w)

	for name, id := range map[string]string{"live": live.ID, "accepted": accepted.ID} {
		var count int
		err := conn.QueryRowContext(ctx,
			`SELECT count(*) FROM system.auth_audit_log WHERE event_type = 'user.invite_expired' AND metadata->>'invitation_id' = $1`,
			id,
		).Scan(&count)
		if err != nil {
			t.Fatalf("count auth_audit_log rows for %s: %v", name, err)
		}
		if count != 0 {
			t.Errorf("expected no user.invite_expired row for the %s invitation, got %d", name, count)
		}
	}
}
