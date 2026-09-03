package tenantconfig

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// createModuleConfigSchema stands up tenant_{slug} plus a minimal
// module_config table directly, rather than pulling in the
// tenant/provision package's own DDL — this test only needs a place to
// write one module_config row, not real provisioning.
func (e *testEnv) createModuleConfigSchema(t *testing.T, slug string) {
	t.Helper()
	ctx := context.Background()
	schema := tenantschema.Name(slug)

	if _, err := e.conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })

	createTable := fmt.Sprintf(`
		CREATE TABLE %s.module_config (
		    module_name TEXT NOT NULL,
		    key         TEXT NOT NULL,
		    value       JSONB NOT NULL,
		    value_type  TEXT NOT NULL,
		    PRIMARY KEY (module_name, key)
		)`, schema)
	if _, err := e.conn.ExecContext(ctx, createTable); err != nil {
		t.Fatalf("create module_config table: %v", err)
	}
}

func (e *testEnv) setModuleConfig(t *testing.T, slug, moduleName, key, jsonValue string) {
	t.Helper()
	query := fmt.Sprintf(`INSERT INTO %s.module_config (module_name, key, value, value_type) VALUES ($1, $2, $3, 'string')`, tenantschema.Name(slug))
	if _, err := e.conn.ExecContext(context.Background(), query, moduleName, key, jsonValue); err != nil {
		t.Fatalf("insert module_config row: %v", err)
	}
}

func testRegistryWithSeed(moduleName, key string, seedValue any) *registry.ModuleRegistry {
	reg := &registry.ModuleRegistry{}
	_, _ = reg.Update(map[string]*module.LoadedModule{
		moduleName: {
			Status:       module.StatusReady,
			Manifest:     manifest.Manifest{Name: moduleName, TenantConfigSeeds: map[string]any{key: seedValue}},
			Capabilities: abi.CapDBRead,
		},
	})
	return reg
}

func TestResolver_Get_PriorityOrder_OverrideBeatsModuleConfigBeatsDefault(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	env.createModuleConfigSchema(t, tt.Slug)
	env.setModuleConfig(t, tt.Slug, "contacts", "default_country_code", `"FR"`)

	reg := testRegistryWithSeed("contacts", "default_country_code", "US")
	resolver := NewResolver(env.store, env.tenantStore, reg)

	// With only module_config and the manifest default present, the
	// tenant-admin-set module_config value wins.
	value, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !ok || value != "FR" {
		t.Fatalf("Get() = %q, %v, want %q, true (module_config beats manifest default)", value, ok, "FR")
	}

	// Once an operator override exists, it wins over both.
	if err := env.store.Set(context.Background(), tt.ID, "contacts.default_country_code", "DE"); err != nil {
		t.Fatalf("Set() override error: %v", err)
	}
	resolver.cache = map[string]cachedValue{} // bypass the cache to observe the new resolution

	value, ok, err = resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !ok || value != "DE" {
		t.Fatalf("Get() = %q, %v, want %q, true (override beats module_config)", value, ok, "DE")
	}
}

func TestResolver_Get_FallsBackToManifestDefault(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	env.createModuleConfigSchema(t, tt.Slug)

	reg := testRegistryWithSeed("contacts", "default_country_code", "US")
	resolver := NewResolver(env.store, env.tenantStore, reg)

	value, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !ok || value != "US" {
		t.Fatalf("Get() = %q, %v, want %q, true", value, ok, "US")
	}
}

func TestResolver_Get_NoneOfTheThreeSources_NotFound(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	env.createModuleConfigSchema(t, tt.Slug)

	resolver := NewResolver(env.store, env.tenantStore, &registry.ModuleRegistry{})

	value, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if ok || value != "" {
		t.Fatalf("Get() = %q, %v, want \"\", false", value, ok)
	}
}

func TestResolver_Get_CachesResolvedValue(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	env.createModuleConfigSchema(t, tt.Slug)

	if err := env.store.Set(context.Background(), tt.ID, "contacts.default_country_code", "DE"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	resolver := NewResolver(env.store, env.tenantStore, &registry.ModuleRegistry{})
	value, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
	if err != nil || !ok || value != "DE" {
		t.Fatalf("first Get() = %q, %v, %v, want %q, true, nil", value, ok, err, "DE")
	}

	// Change the underlying override directly; a cache hit must still
	// serve the stale value until the entry's TTL expires.
	if err := env.store.Set(context.Background(), tt.ID, "contacts.default_country_code", "FR"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	value, ok, err = resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
	if err != nil || !ok || value != "DE" {
		t.Fatalf("second Get() = %q, %v, %v, want cached %q, true, nil", value, ok, err, "DE")
	}
}

func TestResolver_CachedGet_EvictsExpiredEntry(t *testing.T) {
	resolver := NewResolver(nil, nil, nil)
	resolver.cache["k"] = cachedValue{value: "stale", found: true, expiresAt: time.Now().Add(-time.Second)}

	if _, ok := resolver.cachedGet("k"); ok {
		t.Fatal("cachedGet() = true for an expired entry, want false")
	}
	if _, stillThere := resolver.cache["k"]; stillThere {
		t.Fatal("expired entry was not evicted from the cache map")
	}
}
