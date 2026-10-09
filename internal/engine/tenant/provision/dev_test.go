package tenantprovision

import (
	"database/sql"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func devFixture(t *testing.T) (*DevBootstrap, *sql.DB) {
	t.Helper()
	admin := membershiptest.New(t)
	var database string
	if err := admin.QueryRowContext(t.Context(), "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	ddl := membershiptest.Open(t, database, "schema_sync_user")
	primary := membershiptest.Open(t, database, "engine_user")
	for _, bootstrap := range []func() error{
		func() error { return billing.NewStore(ddl).Bootstrap(t.Context()) },
		func() error { return providerselect.NewStore(ddl).Bootstrap(t.Context()) },
	} {
		if err := bootstrap(); err != nil {
			t.Fatal(err)
		}
	}
	syncPool := schema.NewPool(ddl, 5*time.Second)
	if err := syncPool.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"sales": {
			Manifest: manifest.Manifest{
				Name:    "sales",
				Version: "0.1.0",
				Permissions: []manifest.Permission{
					{Name: "sales:widget:read", DefaultRoles: []string{"admin"}},
				},
			},
			ModelDecls: []model.ModelDeclaration{widgetModel()},
		},
	}); err != nil {
		t.Fatal(err)
	}
	roles := role.NewStore(primary)
	activities := NewActivities(tenant.NewStore(primary), nil, roles, ddl, syncPool, schema.NewSchemaDiffEngine(&schema.Config{}), reg, "localhost", nil, []string{"en"})
	bootstrap := NewDevBootstrap(activities, user.NewStore(primary), billing.NewStore(primary), password.NewHasher(64, time.Second), permcache.NewRolePermissionMap())

	return bootstrap, admin
}

func TestDevBootstrapIdempotentWithoutTemporal(t *testing.T) {
	d, conn := devFixture(t)
	first, err := d.BootstrapDev(t.Context(), "sales")
	if err != nil {
		t.Fatal(err)
	}
	u, err := d.users.GetByEmail(t.Context(), first.Email)
	if err != nil {
		t.Fatal(err)
	}
	originalHash := *u.PasswordHash
	if _, err := conn.ExecContext(t.Context(), "INSERT INTO tenant_dev.widgets (tenant_id, name) VALUES ($1, 'retained')", first.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "DELETE FROM system.user_profiles WHERE user_id = $1", u.ID); err != nil {
		t.Fatal(err)
	}

	second, err := d.BootstrapDev(t.Context(), "sales")
	if err != nil || first != second {
		t.Fatalf("repeated bootstrap: first=%+v second=%+v err=%v", first, second, err)
	}
	u, err = d.users.GetByEmail(t.Context(), second.Email)
	if err != nil || *u.PasswordHash != originalHash {
		t.Fatalf("existing credentials changed: %v", err)
	}
	if _, err := d.users.GetProfile(t.Context(), u.ID); err != nil {
		t.Fatalf("incomplete admin profile was not recovered: %v", err)
	}
	var retained int
	if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM tenant_dev.widgets WHERE name = 'retained'").Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("dev data was not retained: %d %v", retained, err)
	}

	tenantRecord, err := d.activities.tenantStore.GetBySlug(t.Context(), devSlug)
	if err != nil || tenantRecord.Status != tenant.StatusActive {
		t.Fatalf("dev tenant is not active: %+v %v", tenantRecord, err)
	}
	domains, err := d.activities.tenantStore.DomainsForTenant(t.Context(), first.TenantID)
	if err != nil || len(domains) != 1 || domains[0].Domain != "dev.localhost" {
		t.Fatalf("dev domain: %+v %v", domains, err)
	}
	overrides, err := d.billing.ActiveOverridesForTenant(t.Context(), first.TenantID)
	if err != nil || len(overrides) != 1 || overrides[0].Feature != "module.sales" || overrides[0].Value != "true" {
		t.Fatalf("module entitlement: %+v %v", overrides, err)
	}
	adminID, err := d.activities.roleStore.AdminUserID(t.Context(), devSlug)
	if err != nil || adminID != u.ID {
		t.Fatalf("founding admin: %s %v", adminID, err)
	}
	roleID, err := d.activities.roleStore.GetRoleByName(t.Context(), devSlug, "admin")
	if err != nil {
		t.Fatal(err)
	}
	bits, ok := d.roles.Lookup(roleID)
	index, indexed := d.activities.registry.Snapshot().PermissionRegistry().Index("sales:widget:read")
	if !ok || !indexed || !bits.Has(index) {
		t.Fatal("dev admin permissions were not published")
	}
}

func TestDevBootstrapRejectsUnrelatedTenantAndCredentials(t *testing.T) {
	d, conn := devFixture(t)
	if _, err := d.BootstrapDev(t.Context(), "missing"); err == nil {
		t.Fatal("enabled an unloaded module")
	}
	if _, err := d.activities.tenantStore.CreateTenant(t.Context(), devSlug, "Unrelated tenant"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.BootstrapDev(t.Context(), "sales"); err == nil {
		t.Fatal("adopted an unrelated tenant")
	}
	if _, err := conn.ExecContext(t.Context(), "UPDATE system.tenants SET name = $1 WHERE slug = 'dev'", devName); err != nil {
		t.Fatal(err)
	}
	slot, err := d.hasher.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := slot.Hash("Unrelated-Password-2026!")
	slot.Release()
	if err != nil {
		t.Fatal(err)
	}
	userID, err := d.users.CreateRegistered(t.Context(), devEmail, hash, user.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.BootstrapDev(t.Context(), "sales"); err == nil {
		t.Fatal("overwrote an existing account password")
	}
	u, err := d.users.GetByID(t.Context(), userID)
	if err != nil || *u.PasswordHash != hash {
		t.Fatal("rejected bootstrap changed credentials")
	}
}

func TestDevBootstrapSyncsSourceChangesAtSameVersion(t *testing.T) {
	d, conn := devFixture(t)
	if _, err := d.BootstrapDev(t.Context(), "sales"); err != nil {
		t.Fatal(err)
	}

	mod := *d.activities.registry.Snapshot().Modules()["sales"]
	declaration := model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("description", model.Text())
	mod.ModelDecls = []model.ModelDeclaration{*declaration}
	if _, err := d.activities.registry.Update(map[string]*module.LoadedModule{"sales": &mod}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.BootstrapDev(t.Context(), "sales"); err != nil {
		t.Fatal(err)
	}

	var exists bool
	err := conn.QueryRowContext(t.Context(), `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'tenant_dev' AND table_name = 'widgets' AND column_name = 'description'
	)`).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("same-version source field was not synchronized: exists=%v err=%v", exists, err)
	}
}
