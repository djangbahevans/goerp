package loader

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

// TestLoadModule_RouteForbiddenTypes_RejectRoutes exercises
// validateModuleRoutes (manifest-spec.md §3's "may register routes"
// column): of the 8 module types, only domain and connector allow
// registering routes — l10n, bridge, theme, report_bundle, automation,
// and field_extension all forbid it, and a module of one of those types
// whose get_routes() export returns a non-empty route set must fail to
// load, while one returning an empty route set loads normally. okModule/oneRouteModule
// are the same hand-crafted WASM fixtures TestLoadModule_Success and
// TestLoadModule_DecodesRealRouteFromGetRoutes already use, so this needs
// no new binary fixture — only the manifest's type/required-field extras
// differ, mirroring manifest_test.go's validTypeFields per-type extras.
func TestLoadModule_RouteForbiddenTypes_RejectRoutes(t *testing.T) {
	tests := map[string]struct {
		wasmBytes []byte
		extra     map[string]any
		wantFail  bool
	}{
		"l10n with no routes loads normally": {
			okModule,
			map[string]any{"type": "l10n"},
			false,
		},
		"l10n with a route fails": {
			oneRouteModule,
			map[string]any{"type": "l10n"},
			true,
		},
		"bridge with no routes loads normally": {
			okModule,
			map[string]any{"type": "bridge", "depends_on": []string{"mod_a", "mod_b"}},
			false,
		},
		"bridge with a route fails": {
			oneRouteModule,
			map[string]any{"type": "bridge", "depends_on": []string{"mod_a", "mod_b"}},
			true,
		},
		"theme with no routes loads normally": {
			okModule,
			map[string]any{
				"type":  "theme",
				"wasm":  false,
				"views": []map[string]any{{"name": "settings", "type": "list", "resource": "theme.settings", "label": "Settings"}},
			},
			false,
		},
		"theme with a route fails": {
			oneRouteModule,
			map[string]any{
				"type":  "theme",
				"wasm":  false,
				"views": []map[string]any{{"name": "settings", "type": "list", "resource": "theme.settings", "label": "Settings"}},
			},
			true,
		},
		"report_bundle with no routes loads normally": {
			okModule,
			map[string]any{
				"type":    "report_bundle",
				"reports": []map[string]any{{"name": "r", "label": "R", "template": "t", "data_handler": "h", "formats": []string{"pdf"}, "permissions": []string{}}},
			},
			false,
		},
		"report_bundle with a route fails": {
			oneRouteModule,
			map[string]any{
				"type":    "report_bundle",
				"reports": []map[string]any{{"name": "r", "label": "R", "template": "t", "data_handler": "h", "formats": []string{"pdf"}, "permissions": []string{}}},
			},
			true,
		},
		"automation with no routes loads normally": {
			okModule,
			map[string]any{"type": "automation", "subscribes": []map[string]any{{"name": "widgets.thing.created"}}},
			false,
		},
		"automation with a route fails": {
			oneRouteModule,
			map[string]any{"type": "automation", "subscribes": []map[string]any{{"name": "widgets.thing.created"}}},
			true,
		},
		"field_extension with no routes loads normally": {
			okModule,
			map[string]any{
				"type":            "field_extension",
				"view_extensions": []map[string]any{{"extends": "core", "extension": "ext"}},
				"schema": map[string]any{
					"owned_models":   []string{},
					"extends_module": "core",
					"extends_models": []string{"sales.order"},
				},
			},
			false,
		},
		"field_extension with a route fails": {
			oneRouteModule,
			map[string]any{
				"type":            "field_extension",
				"view_extensions": []map[string]any{{"extends": "core", "extension": "ext"}},
				"schema": map[string]any{
					"owned_models":   []string{},
					"extends_module": "core",
					"extends_models": []string{"sales.order"},
				},
			},
			true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t)
			src := Source{
				Name:          "widgets",
				ManifestBytes: manifestJSONWithFields(t, "widgets", tt.wasmBytes, []string{"db.read", "event.emit"}, tt.extra),
				WasmBytes:     tt.wasmBytes,
			}

			m := LoadModule(context.Background(), rt, testPoolCfg(), src)

			if tt.wantFail {
				if m.Status != module.StatusFailed {
					t.Fatalf("Status = %v, want StatusFailed", m.Status)
				}
				if !strings.Contains(m.FailureReason, "must not register routes") {
					t.Errorf("FailureReason = %q, want it to mention route registration", m.FailureReason)
				}
				return
			}

			if m.Status != module.StatusSyncing {
				t.Fatalf("Status = %v, want StatusSyncing; FailureReason = %q", m.Status, m.FailureReason)
			}
			t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })
		})
	}
}
