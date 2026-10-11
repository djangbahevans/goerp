package tenantsync

import (
	"fmt"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestSyncConfigSeedsOnlyOnInitialInstall(t *testing.T) {
	env := newTestEnv(t)
	tenant := env.activeTenant(t, uniqueSlug(t))
	for _, group := range enginetables.Groups {
		if slices.ContainsFunc(group.Tables, func(table enginetables.Table) bool { return table.Name == "module_config" }) {
			if err := group.Create(t.Context(), env.conn, tenant.Slug); err != nil {
				t.Fatal(err)
			}
		}
	}

	mod := loadedModule(t, "config_"+tenant.Slug)
	mod.Manifest.ConfigSchema = []manifest.ConfigEntry{{Key: "country", Type: "string"}, {Key: "reset", Type: "string"}}
	mod.Manifest.TenantConfigSeeds = map[string]any{
		mod.Manifest.Name + ".country": "GH",
		mod.Manifest.Name + ".reset":   "default",
	}

	query := fmt.Sprintf("ALTER TABLE %s.module_config ADD CONSTRAINT reject_seed CHECK (key <> 'reset')", tenantschema.Name(tenant.Slug))
	if _, err := env.conn.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	if err := SyncOne(t.Context(), env.pool, env.diffEngine, tenant, mod, nil); err == nil {
		t.Fatal("initial sync succeeded despite rejected seed")
	}
	if moduleSyncRecorded(t, env.conn, tenant.ID, mod.Manifest.Name) {
		t.Fatal("failed initial seed recorded successful installation")
	}
	query = fmt.Sprintf("ALTER TABLE %s.module_config DROP CONSTRAINT reject_seed", tenantschema.Name(tenant.Slug))
	if _, err := env.conn.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}

	if err := SyncOne(t.Context(), env.pool, env.diffEngine, tenant, mod, nil); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	store := tenantconfig.NewStore(env.conn)
	rows, err := store.ModuleConfigRows(t.Context(), tenantschema.Name(tenant.Slug), mod.Manifest.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows["country"].Value != `"GH"` {
		t.Fatalf("initial seeds = %v, want short-key seed rows", rows)
	}

	query = fmt.Sprintf("UPDATE %s.module_config SET value = '\"KE\"' WHERE module_name = $1 AND key = 'country'", tenantschema.Name(tenant.Slug))
	if _, err := env.conn.ExecContext(t.Context(), query, mod.Manifest.Name); err != nil {
		t.Fatal(err)
	}
	query = fmt.Sprintf("DELETE FROM %s.module_config WHERE module_name = $1 AND key = 'reset'", tenantschema.Name(tenant.Slug))
	if _, err := env.conn.ExecContext(t.Context(), query, mod.Manifest.Name); err != nil {
		t.Fatal(err)
	}

	mod.Manifest.Version = "1.1.0"
	mod.Manifest.ConfigSchema = append(mod.Manifest.ConfigSchema, manifest.ConfigEntry{Key: "new", Type: "boolean"})
	mod.Manifest.TenantConfigSeeds[mod.Manifest.Name+".country"] = "US"
	mod.Manifest.TenantConfigSeeds[mod.Manifest.Name+".new"] = true
	if err := SyncOne(t.Context(), env.pool, env.diffEngine, tenant, mod, nil); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if err := SyncOneUnversioned(t.Context(), env.pool, env.diffEngine, tenant, mod); err != nil {
		t.Fatalf("unversioned resync: %v", err)
	}

	rows, err = store.ModuleConfigRows(t.Context(), tenantschema.Name(tenant.Slug), mod.Manifest.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows["country"].Value != `"KE"` {
		t.Fatalf("upgrade reapplied seeds or replaced override: %v", rows)
	}
}
