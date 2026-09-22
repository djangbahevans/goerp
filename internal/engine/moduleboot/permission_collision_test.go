package moduleboot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
)

// manifestJSONWithPermissions is manifestJSON plus a permissions[] list —
// manifestJSON itself takes no extra-fields parameter, unlike loader_test.go's
// manifestJSONWithFields.
func manifestJSONWithPermissions(t *testing.T, name string, wasmBytes []byte, dependsOn []string, permissions []map[string]any) []byte {
	t.Helper()
	sum := sha256.Sum256(wasmBytes)
	if dependsOn == nil {
		dependsOn = []string{}
	}

	fields := map[string]any{
		"name":         name,
		"display_name": name,
		"type":         "domain",
		"version":      "1.0.0",
		"description":  "a test module",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   dependsOn,
		"capabilities": []string{},
		"schema": map[string]any{
			"owned_models": []string{},
		},
		"checksum":    fmt.Sprintf("sha256:%x", sum),
		"permissions": permissions,
	}

	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal manifest fixture: %v", err)
	}
	return data
}

func TestLoadCascading_PermissionNameCollision_LaterModuleFailsEarlierUnaffected(t *testing.T) {
	rt := newTestRuntime(t)

	sources := []loader.Source{
		{
			Name: "crm",
			ManifestBytes: manifestJSONWithPermissions(t, "crm", okModule, nil,
				[]map[string]any{{"name": "crm:contact:read", "description": "Read contacts"}}),
			WasmBytes: okModule,
		},
		{
			Name: "sales",
			ManifestBytes: manifestJSONWithPermissions(t, "sales", okModule, nil,
				[]map[string]any{{"name": "crm:contact:read", "description": "Also read contacts"}}),
			WasmBytes: okModule,
		},
	}

	modules := LoadCascading(context.Background(), rt, testPoolCfg(), sources)

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
