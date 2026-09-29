package membership

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

type env struct {
	conn    *sql.DB
	tenants *tenant.Store
	roles   *role.Store
	users   *user.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	e := &env{conn: conn, tenants: tenant.NewStore(conn), roles: role.NewStore(conn), users: user.NewStore(conn)}
	for name, bootstrap := range map[string]func(context.Context) error{
		"tenant": e.tenants.Bootstrap, "user": e.users.Bootstrap,
	} {
		if err := bootstrap(t.Context()); err != nil {
			t.Fatalf("%s Bootstrap() error: %v", name, err)
		}
	}
	if err := e.roles.BootstrapMembershipIndex(t.Context()); err != nil {
		t.Fatalf("BootstrapMembershipIndex() error: %v", err)
	}
	return e
}

// newTenant creates an active tenant with the role tables and the
// membership trigger, as provisioning does.
func (e *env) newTenant(t *testing.T) *tenant.Tenant {
	t.Helper()
	ctx := t.Context()
	slug := fmt.Sprintf("membership%d", time.Now().UnixNano())
	tt, err := e.tenants.CreateTenant(ctx, slug, "Membership Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
	if _, err := e.tenants.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate tenant: %v", err)
	}
	schema := tenantschema.Name(slug)
	if _, err := e.conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })
	if err := e.roles.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := e.roles.AttachMembershipTrigger(ctx, slug); err != nil {
		t.Fatalf("AttachMembershipTrigger() error: %v", err)
	}
	if err := e.roles.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	return tt
}

func (e *env) newUser(t *testing.T) string {
	t.Helper()
	id, err := e.users.FindOrCreateInvited(t.Context(), fmt.Sprintf("membership%d@example.com", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec(`DELETE FROM system.users WHERE id = $1`, id) })
	return id
}

func (e *env) join(t *testing.T, tt *tenant.Tenant, userID string) {
	t.Helper()
	ctx := t.Context()
	roleID, err := e.roles.GetRoleByName(ctx, tt.Slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	if err := e.roles.AddMember(ctx, tt.Slug, userID); err != nil {
		t.Fatalf("AddMember() error: %v", err)
	}
	if err := e.roles.AssignRole(ctx, tt.Slug, userID, roleID, ""); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}
}

func (e *env) indexed(t *testing.T, userID string) []string {
	t.Helper()
	ids, err := e.roles.CandidateTenantIDs(t.Context(), userID)
	if err != nil {
		t.Fatalf("CandidateTenantIDs() error: %v", err)
	}
	slices.Sort(ids)
	return ids
}

func (e *env) tenantsOf(t *testing.T, userID string) []string {
	t.Helper()
	got, err := TenantsOf(t.Context(), e.tenants, e.roles, userID)
	if err != nil {
		t.Fatalf("TenantsOf() error: %v", err)
	}
	slugs := make([]string, len(got))
	for i, tt := range got {
		slugs[i] = tt.Slug
	}
	return slugs
}

func sorted(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

func TestTrigger_KeepsTheIndexInStepWithMemberRows(t *testing.T) {
	e := newEnv(t)
	a, b := e.newTenant(t), e.newTenant(t)
	userID := e.newUser(t)

	e.join(t, a, userID)
	e.join(t, b, userID)
	if got, want := e.indexed(t, userID), sorted(a.ID, b.ID); !slices.Equal(got, want) {
		t.Fatalf("index after joining = %v, want %v", got, want)
	}

	tx, err := e.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error: %v", err)
	}
	if err := role.RemoveMemberTx(t.Context(), tx, a.Slug, userID); err != nil {
		t.Fatalf("RemoveMemberTx() error: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error: %v", err)
	}
	if got, want := e.indexed(t, userID), []string{b.ID}; !slices.Equal(got, want) {
		t.Errorf("index after removal from A = %v, want %v", got, want)
	}
}

func TestTenantsOf_ConfirmsEachCandidate(t *testing.T) {
	e := newEnv(t)
	a, b, c := e.newTenant(t), e.newTenant(t), e.newTenant(t)
	userID := e.newUser(t)
	e.join(t, a, userID)
	e.join(t, b, userID)
	e.join(t, c, userID)

	if got, want := e.tenantsOf(t, userID), sorted(a.Slug, b.Slug, c.Slug); !slices.Equal(got, want) {
		t.Fatalf("TenantsOf() = %v, want %v", got, want)
	}

	// Suspended in B, and only an expired grant left in C: the index still
	// lists both, but neither passes the membership check.
	if _, err := e.conn.Exec(fmt.Sprintf(`UPDATE %s.tenant_members SET status = 'suspended' WHERE user_id = $1`, tenantschema.Name(b.Slug)), userID); err != nil {
		t.Fatalf("suspend in B: %v", err)
	}
	if _, err := e.conn.Exec(fmt.Sprintf(`UPDATE %s.user_roles SET expires_at = NOW() - interval '1 hour' WHERE user_id = $1`, tenantschema.Name(c.Slug)), userID); err != nil {
		t.Fatalf("expire grant in C: %v", err)
	}
	if got := e.indexed(t, userID); len(got) != 3 {
		t.Errorf("index = %v, want all three tenants still listed", got)
	}
	if got, want := e.tenantsOf(t, userID), []string{a.Slug}; !slices.Equal(got, want) {
		t.Errorf("TenantsOf() = %v, want %v", got, want)
	}
	for _, tt := range []*tenant.Tenant{b, c} {
		if ok, err := e.roles.IsMember(t.Context(), tt.Slug, userID); err != nil || ok {
			t.Errorf("IsMember(%s) = %v (err %v), want false", tt.Slug, ok, err)
		}
	}
	if ids, err := e.roles.RoleIDsForUser(t.Context(), b.Slug, userID); err != nil || len(ids) != 0 {
		t.Errorf("RoleIDsForUser() for a suspended member = %v (err %v), want none", ids, err)
	}
}

func TestTenantsOf_AGrantWithoutAMemberRowIsNotMembership(t *testing.T) {
	e := newEnv(t)
	a := e.newTenant(t)
	userID := e.newUser(t)
	roleID, err := e.roles.GetRoleByName(t.Context(), a.Slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	if err := e.roles.AssignRole(t.Context(), a.Slug, userID, roleID, ""); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}
	// A stale index row with no member row behind it grants nothing.
	if _, err := e.conn.Exec(`INSERT INTO system.tenant_memberships (user_id, tenant_id) VALUES ($1, $2)`, userID, a.ID); err != nil {
		t.Fatalf("insert stale index row: %v", err)
	}
	if ok, err := e.roles.IsMember(t.Context(), a.Slug, userID); err != nil || ok {
		t.Errorf("IsMember() = %v (err %v), want false", ok, err)
	}
	if got := e.tenantsOf(t, userID); len(got) != 0 {
		t.Errorf("TenantsOf() = %v, want none", got)
	}
}

func TestReindexMemberships_RestoresTheIndexFromMemberRows(t *testing.T) {
	e := newEnv(t)
	a := e.newTenant(t)
	userID := e.newUser(t)
	e.join(t, a, userID)
	if _, err := e.conn.Exec(`DELETE FROM system.tenant_memberships WHERE tenant_id = $1`, a.ID); err != nil {
		t.Fatalf("clear index: %v", err)
	}

	for range 2 {
		if err := e.roles.ReindexMemberships(t.Context(), a.ID, a.Slug); err != nil {
			t.Fatalf("ReindexMemberships() error: %v", err)
		}
	}
	if got, want := e.indexed(t, userID), []string{a.ID}; !slices.Equal(got, want) {
		t.Errorf("index = %v, want %v", got, want)
	}
}

func TestDrop_RemovesTheTenantsIndexRows(t *testing.T) {
	e := newEnv(t)
	a, b := e.newTenant(t), e.newTenant(t)
	userID := e.newUser(t)
	e.join(t, a, userID)
	e.join(t, b, userID)

	if err := tenantschema.Drop(t.Context(), e.conn, a.Slug); err != nil {
		t.Fatalf("Drop() error: %v", err)
	}
	if got, want := e.indexed(t, userID), []string{b.ID}; !slices.Equal(got, want) {
		t.Errorf("index after dropping A = %v, want %v", got, want)
	}
}
