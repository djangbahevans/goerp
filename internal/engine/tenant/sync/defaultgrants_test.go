package tenantsync

import (
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/role"
)

func adminGrants(t *testing.T, env *testEnv, slug string) []string {
	t.Helper()
	rows, err := env.conn.QueryContext(t.Context(), `
		SELECT rp.permission_name
		FROM tenant_`+slug+`.role_permissions rp
		JOIN tenant_`+slug+`.roles r ON r.id = rp.role_id
		WHERE r.name = 'admin'
		ORDER BY rp.permission_name`)
	if err != nil {
		t.Fatalf("query admin grants: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

func TestSyncOne_GrantsModuleDefaultPermissionsOnApplyingSync(t *testing.T) {
	env := newTestEnv(t)
	tn := env.activeTenant(t, uniqueSlug(t))
	ctx := t.Context()
	roles := role.NewStore(env.conn)
	if err := roles.Bootstrap(ctx, tn.Slug); err != nil {
		t.Fatal(err)
	}
	if err := roles.SeedBuiltinRoles(ctx, tn.Slug); err != nil {
		t.Fatal(err)
	}

	mod := loadedModule(t, "widgets_"+tn.Slug, widgetModel())
	read := mod.Manifest.Name + ":widget:read"
	write := mod.Manifest.Name + ":widget:write"
	mod.Manifest.Permissions = []manifest.Permission{{Name: read, DefaultRoles: []string{"admin"}}}

	if err := SyncOne(ctx, env.pool, env.diffEngine, tn, mod, nil); err != nil {
		t.Fatalf("install module: %v", err)
	}
	if got := adminGrants(t, env, tn.Slug); !slices.Equal(got, []string{read}) {
		t.Fatalf("admin grants after install = %v, want [%s]", got, read)
	}

	if _, err := env.conn.ExecContext(ctx, "DELETE FROM tenant_"+tn.Slug+".role_permissions"); err != nil {
		t.Fatal(err)
	}
	if err := SyncOne(ctx, env.pool, env.diffEngine, tn, mod, nil); err != nil {
		t.Fatalf("resync same version: %v", err)
	}
	if got := adminGrants(t, env, tn.Slug); len(got) != 0 {
		t.Errorf("admin grants after same-version sync = %v, want a removed grant to stay removed", got)
	}

	mod.Manifest.Version = "2.0.0"
	mod.Manifest.Permissions = append(mod.Manifest.Permissions, manifest.Permission{Name: write, DefaultRoles: []string{"admin"}})
	if err := SyncOne(ctx, env.pool, env.diffEngine, tn, mod, nil); err != nil {
		t.Fatalf("upgrade module: %v", err)
	}
	if got := adminGrants(t, env, tn.Slug); !slices.Equal(got, []string{read, write}) {
		t.Errorf("admin grants after upgrade = %v, want [%s %s]", got, read, write)
	}
}

func TestSyncOne_ModuleDefaultPermissionsSkippedWithoutRBACTables(t *testing.T) {
	env := newTestEnv(t)
	tn := env.activeTenant(t, uniqueSlug(t))
	mod := loadedModule(t, "widgets_"+tn.Slug, widgetModel())
	mod.Manifest.Permissions = []manifest.Permission{{Name: mod.Manifest.Name + ":widget:read", DefaultRoles: []string{"admin"}}}

	if err := SyncOne(t.Context(), env.pool, env.diffEngine, tn, mod, nil); err != nil {
		t.Fatalf("sync a tenant without RBAC tables: %v", err)
	}
	if !moduleSyncRecorded(t, env.conn, tn.ID, mod.Manifest.Name) {
		t.Error("sync not recorded")
	}
}
