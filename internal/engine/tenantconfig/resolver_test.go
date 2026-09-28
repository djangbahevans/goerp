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
		    encrypted   BOOLEAN NOT NULL DEFAULT FALSE,
		    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    updated_by  UUID,
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

func (e *testEnv) setEncryptedModuleConfig(t *testing.T, slug, moduleName, key, jsonValue string) {
	t.Helper()
	query := fmt.Sprintf(`INSERT INTO %s.module_config (module_name, key, value, value_type, encrypted) VALUES ($1, $2, $3, 'string', true)`, tenantschema.Name(slug))
	if _, err := e.conn.ExecContext(context.Background(), query, moduleName, key, jsonValue); err != nil {
		t.Fatalf("insert encrypted module_config row: %v", err)
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
	value, _, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
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
	resolver.Invalidate(tt.ID, "contacts.default_country_code") // bypass the cache to observe the new resolution

	value, _, ok, err = resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
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

	value, _, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
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

	value, _, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
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
	value, _, ok, err := resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
	if err != nil || !ok || value != "DE" {
		t.Fatalf("first Get() = %q, %v, %v, want %q, true, nil", value, ok, err, "DE")
	}

	// Change the underlying override directly; a cache hit must still
	// serve the stale value until the entry's TTL expires.
	if err := env.store.Set(context.Background(), tt.ID, "contacts.default_country_code", "FR"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	value, _, ok, err = resolver.Get(context.Background(), tt.ID, "contacts.default_country_code")
	if err != nil || !ok || value != "DE" {
		t.Fatalf("second Get() = %q, %v, %v, want cached %q, true, nil", value, ok, err, "DE")
	}
}

func TestResolver_CacheSetIfFresh_SkipsWriteAfterConcurrentInvalidate(t *testing.T) {
	resolver := NewResolver(nil, nil, nil)
	gen := resolver.currentGeneration()

	// Simulates an Invalidate arriving (e.g. via a Listener notification)
	// while a Get's own DB read, which captured gen before issuing that
	// read, is still in flight.
	resolver.Invalidate("tenant-1", "some.key")

	resolver.cacheSetIfFresh("k", cachedValue{value: "stale-value-read-before-the-invalidate", found: true}, gen)

	if _, ok := resolver.cachedGet("k"); ok {
		t.Fatal("cacheSetIfFresh cached a value read before a concurrent Invalidate, want it skipped")
	}
}

func TestResolver_CacheSetIfFresh_WritesWithNoInterveningInvalidate(t *testing.T) {
	resolver := NewResolver(nil, nil, nil)
	gen := resolver.currentGeneration()

	resolver.cacheSetIfFresh("k", cachedValue{value: "fresh-value", found: true}, gen)

	cached, ok := resolver.cachedGet("k")
	if !ok || cached.value != "fresh-value" {
		t.Fatalf("cacheSetIfFresh() did not cache a read with no intervening invalidation: ok=%v value=%q", ok, cached.value)
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

func TestResolver_Get_ReportsEncryptedOnlyForEncryptedModuleConfigRow(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	env.createModuleConfigSchema(t, tt.Slug)
	env.setEncryptedModuleConfig(t, tt.Slug, "billing", "api_key", `"key-id:bm9uY2U:Y2lwaGVy"`)
	env.setModuleConfig(t, tt.Slug, "billing", "region", `"eu"`)

	reg := testRegistryWithSeed("billing", "currency", "USD")
	resolver := NewResolver(env.store, env.tenantStore, reg)
	ctx := t.Context()

	tests := []struct {
		key           string
		wantValue     string
		wantEncrypted bool
	}{
		{"billing.api_key", "key-id:bm9uY2U:Y2lwaGVy", true},
		{"billing.region", "eu", false},
		{"billing.currency", "USD", false},
	}
	for _, tc := range tests {
		value, encrypted, ok, err := resolver.Get(ctx, tt.ID, tc.key)
		if err != nil || !ok {
			t.Fatalf("Get(%s) = ok %v, err %v", tc.key, ok, err)
		}
		if value != tc.wantValue || encrypted != tc.wantEncrypted {
			t.Errorf("Get(%s) = (%q, encrypted %v), want (%q, encrypted %v)", tc.key, value, encrypted, tc.wantValue, tc.wantEncrypted)
		}
	}

	if err := env.store.Set(ctx, tt.ID, "billing.api_key", "ab:cd:ef"); err != nil {
		t.Fatalf("override Set() error: %v", err)
	}
	resolver.Invalidate(tt.ID, "billing.api_key")
	value, encrypted, _, err := resolver.Get(ctx, tt.ID, "billing.api_key")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if value != "ab:cd:ef" || encrypted {
		t.Errorf("override Get() = (%q, encrypted %v), want (%q, encrypted false)", value, encrypted, "ab:cd:ef")
	}
}

func TestResolver_Get_JSONNullModuleConfigFallsThroughToDefault(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	env.createModuleConfigSchema(t, tt.Slug)
	env.setModuleConfig(t, tt.Slug, "contacts", "default_country_code", `null`)

	resolver := NewResolver(env.store, env.tenantStore, testRegistryWithSeed("contacts", "default_country_code", "US"))
	value, _, ok, err := resolver.Get(t.Context(), tt.ID, "contacts.default_country_code")
	if err != nil || !ok || value != "US" {
		t.Errorf("Get() = (%q, ok %v, err %v), want the manifest default %q", value, ok, err, "US")
	}
}
