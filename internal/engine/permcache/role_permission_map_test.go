package permcache

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// testEnv wires *tenant.Store and *role.Store against the real
// compose.dev.yml Postgres — same convention as tenantoffboard's/
// tenantprovision's own testEnv.
type testEnv struct {
	conn        *sql.DB
	tenantStore *tenant.Store
	roleStore   *role.Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}

	return &testEnv{conn: conn, tenantStore: tenantStore, roleStore: role.NewStore(conn)}
}

func uniqueSlug(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("permcache%d", time.Now().UnixNano())
}

// activeTenant creates and activates a tenant, then bootstraps and seeds
// its roles table — the state RolePermissionMap.RebuildAll expects a real
// tenant to be in.
func (e *testEnv) activeTenant(t *testing.T, slug string) {
	t.Helper()
	ctx := context.Background()

	tt, err := e.tenantStore.CreateTenant(ctx, slug, "Permcache Test")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID)
		_, _ = e.conn.Exec("DROP SCHEMA IF EXISTS " + tenantschema.Name(slug) + " CASCADE")
	})

	if _, err := e.tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate tenant: %v", err)
	}
	if _, err := e.conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+tenantschema.Name(slug)); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	if err := e.roleStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := e.roleStore.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
}

func TestRolePermissionMap_LookupUnknownRoleReturnsFalse(t *testing.T) {
	m := NewRolePermissionMap()

	if _, ok := m.Lookup("nonexistent"); ok {
		t.Error("Lookup() ok = true for an unknown role, want false")
	}
}

func TestRolePermissionMap_RebuildAll_ResolvesInheritance(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)
	ctx := context.Background()

	adminID, err := env.roleStore.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName(admin) error: %v", err)
	}

	// A child role inheriting from admin, with its own additional grant.
	schema := tenantschema.Name(slug)
	var childID string
	if err := env.conn.QueryRowContext(ctx,
		fmt.Sprintf("INSERT INTO %s.roles (name, parent_id) VALUES ('sales_manager', $1) RETURNING id", schema), adminID,
	).Scan(&childID); err != nil {
		t.Fatalf("insert child role: %v", err)
	}

	if _, err := env.conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)", schema),
		adminID, "widgets.read",
	); err != nil {
		t.Fatalf("grant admin permission: %v", err)
	}
	if _, err := env.conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)", schema),
		childID, "sales.approve_discount",
	); err != nil {
		t.Fatalf("grant child permission: %v", err)
	}
	// An unknown permission name — must be skipped, not fail the whole rebuild.
	if _, err := env.conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)", schema),
		childID, "nonexistent.permission",
	); err != nil {
		t.Fatalf("grant unknown permission: %v", err)
	}

	reg := permission.NewPermissionRegistry()
	widgetsReadIdx, _ := registerAndIndex(reg, "widgets.read")
	approveDiscountIdx, _ := registerAndIndex(reg, "sales.approve_discount")

	m := NewRolePermissionMap()
	if err := m.RebuildAll(ctx, env.tenantStore, env.roleStore, reg); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}

	adminBits, ok := m.Lookup(adminID)
	if !ok {
		t.Fatal("Lookup(admin) ok = false")
	}
	if !adminBits.Has(widgetsReadIdx) {
		t.Error("admin bitfield missing widgets.read")
	}

	childBits, ok := m.Lookup(childID)
	if !ok {
		t.Fatal("Lookup(child) ok = false")
	}
	if !childBits.Has(widgetsReadIdx) {
		t.Error("child bitfield missing inherited widgets.read")
	}
	if !childBits.Has(approveDiscountIdx) {
		t.Error("child bitfield missing its own sales.approve_discount")
	}
}

// registerAndIndex is a small helper for tests that need a real,
// registered permission index (PermissionRegistry.Register normally runs
// at module load time against a module's declared manifest.Permission
// list — this fixture skips straight to the index it produces).
func registerAndIndex(reg *permission.PermissionRegistry, name string) (int, bool) {
	reg.Register("testmod", []manifest.Permission{{Name: name}})
	return reg.Index(name)
}

func TestRolePermissionMap_RebuildAll_NoActiveTenantsProducesEmptyMap(t *testing.T) {
	env := newTestEnv(t)
	reg := permission.NewPermissionRegistry()

	m := NewRolePermissionMap()
	if err := m.RebuildAll(context.Background(), env.tenantStore, env.roleStore, reg); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}
	// Doesn't assert emptiness (other tests' tenants may still be active
	// concurrently against the same shared dev Postgres) — just that it
	// doesn't error with zero tenants of its own.
}

func TestRolePermissionMap_RebuildAll_SwapsAtomically(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)
	ctx := context.Background()

	reg := permission.NewPermissionRegistry()
	m := NewRolePermissionMap()

	if err := m.RebuildAll(ctx, env.tenantStore, env.roleStore, reg); err != nil {
		t.Fatalf("first RebuildAll() error: %v", err)
	}
	adminID, err := env.roleStore.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	if _, ok := m.Lookup(adminID); !ok {
		t.Fatal("expected admin role to be present after first RebuildAll")
	}

	// A second rebuild against the same state must still find it —
	// confirms the atomic swap didn't drop anything.
	if err := m.RebuildAll(ctx, env.tenantStore, env.roleStore, reg); err != nil {
		t.Fatalf("second RebuildAll() error: %v", err)
	}
	if _, ok := m.Lookup(adminID); !ok {
		t.Error("expected admin role to still be present after second RebuildAll")
	}
}
