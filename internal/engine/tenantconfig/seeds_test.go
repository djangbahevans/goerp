package tenantconfig

import (
	"encoding/json/jsontext"
	"fmt"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestSeedModuleConfigTypesAndRetries(t *testing.T) {
	env := openTestEnv(t)
	tenant := env.createTenant(t)
	env.createModuleConfigSchema(t, tenant.Slug)
	env.setEncryptedModuleConfig(t, tenant.Slug, "demo", "secret", `"tenant-ciphertext"`)

	tests := []struct {
		key   string
		typ   string
		value any
		json  string
	}{
		{key: "country", typ: "string", value: "GH", json: `"GH"`},
		{key: "count", typ: "integer", value: 3, json: `3`},
		{key: "ratio", typ: "float", value: 1.0, json: `1`},
		{key: "enabled", typ: "boolean", value: false, json: `false`},
		{key: "interval", typ: "duration", value: "15m", json: `"15m"`},
		{key: "object", typ: "json", value: map[string]any{"enabled": true}, json: `{"enabled":true}`},
		{key: "null", typ: "json", json: `null`},
		{key: "countries", typ: "string[]", value: []string{"GH", "KE"}, json: `["GH","KE"]`},
		{key: "counts", typ: "integer[]", value: []int{1, 2}, json: `[1,2]`},
		{key: "ratios", typ: "float[]", value: []float64{1, 2.5}, json: `[1,2.5]`},
		{key: "secret", typ: "string", value: "manifest-secret", json: `"tenant-ciphertext"`},
	}
	seed := manifest.Manifest{Name: "demo", TenantConfigSeeds: make(map[string]any)}
	for _, test := range tests {
		seed.ConfigSchema = append(seed.ConfigSchema, manifest.ConfigEntry{Key: test.key, Type: test.typ})
		seed.TenantConfigSeeds["demo."+test.key] = test.value
	}

	if err := env.store.SeedModuleConfig(t.Context(), tenant.Slug, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, err := env.store.ModuleConfigRows(t.Context(), tenantschema.Name(tenant.Slug), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(tests) {
		t.Fatalf("rows = %d, want %d short-key rows", len(rows), len(tests))
	}

	for _, test := range tests {
		row, found := rows[test.key]
		if !found {
			t.Fatalf("missing short key %q: %v", test.key, rows)
		}

		value := jsontext.Value(row.Value)
		if err := value.Compact(); err != nil {
			t.Fatal(err)
		}
		if string(value) != test.json || row.Type != test.typ || row.Encrypted != (test.key == "secret") {
			t.Errorf("row %s = %+v, want value %s and type %s", test.key, row, test.json, test.typ)
		}
	}

	seed.TenantConfigSeeds["demo.country"] = "KE"
	if err := env.store.SeedModuleConfig(t.Context(), tenant.Slug, seed); err != nil {
		t.Fatalf("retry: %v", err)
	}

	after, err := env.store.ModuleConfigRows(t.Context(), tenantschema.Name(tenant.Slug), "demo")
	if err != nil {
		t.Fatal(err)
	}
	for key, before := range rows {
		if got := after[key]; got != before {
			t.Errorf("retry changed %s: before %+v, after %+v", key, before, got)
		}
	}
}

func TestSeedModuleConfigRollsBackOnFailure(t *testing.T) {
	env := openTestEnv(t)
	tenant := env.createTenant(t)
	env.createModuleConfigSchema(t, tenant.Slug)
	query := fmt.Sprintf("ALTER TABLE %s.module_config ADD CHECK (key <> 'rejected')", tenantschema.Name(tenant.Slug))
	if _, err := env.conn.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}

	seed := manifest.Manifest{
		Name: "demo",
		ConfigSchema: []manifest.ConfigEntry{
			{Key: "accepted", Type: "string"},
			{Key: "rejected", Type: "string"},
		},
		TenantConfigSeeds: map[string]any{"demo.accepted": "a", "demo.rejected": "b"},
	}
	if err := env.store.SeedModuleConfig(t.Context(), tenant.Slug, seed); err == nil {
		t.Fatal("seed succeeded despite rejected row")
	}

	rows, err := env.store.ModuleConfigRows(t.Context(), tenantschema.Name(tenant.Slug), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("failed seed left partial rows: %v", rows)
	}
}
