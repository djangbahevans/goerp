package loader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
)

func TestLoadModule_TransitionOverride(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		condition string
		wantError string
	}{
		{mode: "transition"},
		{mode: "none"},
		{mode: "action", wantError: "engine.HandleTransition"},
		{mode: "mismatch", wantError: "does not match a declared workflow transition"},
		{mode: "duplicate", wantError: "registered more than once"},
		{mode: "transition", condition: "record.name LIKE 'A%'", wantError: "LIKE"},
		{mode: "none", condition: "record.name ILIKE 'A%'", wantError: "ILIKE"},
		{mode: "transition", condition: "record child_of record.parent_id", wantError: "child_of"},
		{mode: "none", condition: "record parent_of record.parent_id", wantError: "parent_of"},
		{mode: "transition", condition: "tenant.plan = 'pro'", wantError: "tenant.plan"},
	} {
		t.Run(tc.mode+tc.condition, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "workflow.wasm")
			flags := "-X main.registrationMode=" + tc.mode
			if tc.condition != "" {
				flags += " -X " + strconv.Quote("main.transitionCondition="+tc.condition)
			}
			cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-ldflags", flags, "-o", path, "./testdata/workflowfixture")
			cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build workflow fixture: %v\n%s", err, output)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			wasmtest.Precompile(t, data, wasmtest.SharedCompilationCacheDir())

			rt := newRealFixtureRuntime(t)
			loaded := LoadModule(t.Context(), rt, testPoolCfg(), Source{
				Name:      "sales",
				WasmBytes: data,
				ManifestBytes: manifestJSONWithFields(t, "sales", data, nil, map[string]any{
					"permissions": []map[string]any{{"name": "sales:order:confirm", "label": "Confirm order"}},
				}),
			})
			if tc.wantError != "" {
				if loaded.Status != module.StatusFailed || !strings.Contains(loaded.FailureReason, tc.wantError) ||
					!strings.Contains(loaded.FailureReason, "sales") || !strings.Contains(loaded.FailureReason, "order") || !strings.Contains(loaded.FailureReason, "confirm") {
					t.Fatalf("status=%s reason=%q", loaded.Status, loaded.FailureReason)
				}
				return
			}
			if loaded.Status == module.StatusFailed {
				t.Fatal(loaded.FailureReason)
			}
			t.Cleanup(func() { loaded.Pool.DrainAndClose(context.Background(), 5*time.Second) })

			snap, err := (&registry.ModuleRegistry{}).Update(map[string]*module.LoadedModule{"sales": loaded})
			if err != nil {
				t.Fatal(err)
			}
			entry, _, result, _ := snap.RouteTable().Lookup("POST", "/sales/orders/order-1/confirm")
			if result != route.RouteFound || entry.Manifest.EngineNative != (tc.mode == "none") ||
				entry.Manifest.Workflow == nil || len(entry.Manifest.Permissions) != 1 || entry.Manifest.Permissions[0] != "sales:order:confirm" {
				t.Fatalf("entry=%+v result=%v", entry, result)
			}
		})
	}
}
