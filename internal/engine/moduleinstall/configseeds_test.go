package moduleinstall

import (
	"encoding/json/jsontext"
	"errors"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestWorkerInstallSeedsConfigOnExistingTenant(t *testing.T) {
	env := newTestEnv(t)
	tenant := env.activeTenant(t, uniqueSlug(t))
	for _, group := range enginetables.Groups {
		if slices.ContainsFunc(group.Tables, func(table enginetables.Table) bool { return table.Name == "module_config" }) {
			if err := group.Create(t.Context(), env.conn, tenant.Slug); err != nil {
				t.Fatal(err)
			}
		}
	}

	name := "config_" + tenant.Slug
	store := tenantconfig.NewStore(env.conn)
	if err := store.SetModuleConfig(t.Context(), tenant.ID, tenantschema.Name(tenant.Slug), name, "country", []byte(`"KE"`), "string", false, ""); err != nil {
		t.Fatal(err)
	}

	pkg := buildPackage(t, name, compileFixture(t), map[string]any{
		"config_schema": []map[string]any{
			{"key": "country", "type": "string"},
			{"key": "interval", "type": "duration"},
			{"key": "count", "type": "integer"},
		},
		"tenant_config_seeds": map[string]any{
			name + ".country":  "GH",
			name + ".interval": "15m",
			name + ".count":    jsontext.Value(`9223372036854775807`),
		},
	})
	path := writeTempPackage(t, pkg)
	worker, _ := newWorker(t, env, nil)
	result, err := worker.run(t.Context(), Args{PackagePath: path})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !slices.Contains(result.Succeeded, tenant.Slug) {
		t.Fatalf("tenant absent from successful installs: %+v", result)
	}

	rows, err := store.ModuleConfigRows(t.Context(), tenantschema.Name(tenant.Slug), name)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows["country"].Value != `"KE"` || rows["interval"].Value != `"15m"` || rows["interval"].Type != "duration" {
		t.Fatalf("installed config = %v, want preserved country and seeded interval under short keys", rows)
	}
	if rows["count"].Value != "9223372036854775807" || rows["count"].Type != "integer" {
		t.Fatalf("integer seed lost precision: %+v", rows["count"])
	}

	if _, err := worker.run(t.Context(), Args{PackagePath: path}); !errors.Is(err, errAlreadyLoaded) {
		t.Fatalf("repeat install = %v, want already-loaded rejection", err)
	}
	if !moduleSyncRecorded(t, env.conn, tenant.ID, name) {
		t.Fatal("successful install did not record schema version")
	}
}
