package loader

import (
	"context"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

func TestLoadAll_PermissionNameCollision_LaterModuleFailsEarlierUnaffected(t *testing.T) {
	rt := newTestRuntime(t)

	sources := []Source{
		{
			Name: "crm",
			ManifestBytes: manifestJSONWithFields(t, "crm", okModule, []string{"db.read"}, map[string]any{
				"permissions": []map[string]any{{"name": "crm:contact:read", "description": "Read contacts"}},
			}),
			WasmBytes: okModule,
		},
		{
			Name: "sales",
			ManifestBytes: manifestJSONWithFields(t, "sales", okModule, []string{"db.read"}, map[string]any{
				"permissions": []map[string]any{{"name": "crm:contact:read", "description": "Also read contacts"}},
			}),
			WasmBytes: okModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	if crm := modules["crm"]; crm.Status == module.StatusFailed {
		t.Fatalf("expected the earlier module (crm) to be unaffected, got StatusFailed: %s", crm.FailureReason)
	}

	sales := modules["sales"]
	if sales.Status != module.StatusFailed {
		t.Fatalf("expected the later module (sales) to fail on the permission name collision, got %v", sales.Status)
	}
	if !strings.Contains(sales.FailureReason, `"crm:contact:read"`) || !strings.Contains(sales.FailureReason, "crm") {
		t.Fatalf("expected failure reason to name the permission and the earlier module, got: %q", sales.FailureReason)
	}
}

func TestLoadAll_NoPermissionCollision_BothLoad(t *testing.T) {
	rt := newTestRuntime(t)

	sources := []Source{
		{
			Name: "crm",
			ManifestBytes: manifestJSONWithFields(t, "crm", okModule, []string{"db.read"}, map[string]any{
				"permissions": []map[string]any{{"name": "crm:contact:read", "description": "Read contacts"}},
			}),
			WasmBytes: okModule,
		},
		{
			Name: "sales",
			ManifestBytes: manifestJSONWithFields(t, "sales", okModule, []string{"db.read"}, map[string]any{
				"permissions": []map[string]any{{"name": "sales:order:read", "description": "Read orders"}},
			}),
			WasmBytes: okModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	for name, m := range modules {
		if m.Status == module.StatusFailed {
			t.Fatalf("expected module %q to load, got StatusFailed: %s", name, m.FailureReason)
		}
	}
}
