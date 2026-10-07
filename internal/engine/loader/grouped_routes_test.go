package loader

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

// A real module registering routes on engine.Group has the composed paths
// and the group's middleware, with each route's own options layered on top,
// in the route declarations the engine loads.
func TestLoadModule_GroupedRoutes_CarryGroupMiddleware(t *testing.T) {
	wasmBytes := compileFixture(t, "groupedroutesfixture")
	rt := newRealFixtureRuntime(t)

	m := LoadModule(t.Context(), rt, testPoolCfg(), Source{
		Name: "sales",
		ManifestBytes: manifestJSONWithFields(t, "sales", wasmBytes, []string{}, map[string]any{
			"permissions": []map[string]any{
				{"name": "sales:order:read", "description": "Read orders"},
				{"name": "sales:order:write", "description": "Write orders"},
			},
		}),
		WasmBytes: wasmBytes,
	})
	if m.Status == module.StatusFailed {
		t.Fatalf("Status = StatusFailed, FailureReason = %q", m.FailureReason)
	}
	t.Cleanup(func() { m.Pool.DrainAndClose(context.WithoutCancel(t.Context()), 5*time.Second) })

	type want struct {
		permissions  []string
		maxBodyBytes int
	}
	wants := map[string]want{
		"GET /orders":      {[]string{"sales:order:read"}, 32 * 1024 * 1024},
		"GET /orders/{id}": {[]string{"sales:order:read"}, 32 * 1024 * 1024},
		"POST /orders":     {[]string{"sales:order:read", "sales:order:write"}, 2048},
	}
	if len(m.ExplicitRoutes) != len(wants) {
		t.Fatalf("loaded %d routes, want %d: %+v", len(m.ExplicitRoutes), len(wants), m.ExplicitRoutes)
	}
	for _, r := range m.ExplicitRoutes {
		w, ok := wants[r.Method+" "+r.Path]
		if !ok {
			t.Errorf("unexpected route %s %s", r.Method, r.Path)
			continue
		}
		if !slices.Equal(r.Permissions, w.permissions) {
			t.Errorf("%s %s permissions = %v, want %v", r.Method, r.Path, r.Permissions, w.permissions)
		}
		if r.RateLimit == nil || r.RateLimit.Requests != 10 || r.TimeoutMs != 5000 || r.MaxBodyBytes != w.maxBodyBytes {
			t.Errorf("%s %s rate limit / timeout / body = %+v / %d / %d, want the group's limit and timeout and body %d",
				r.Method, r.Path, r.RateLimit, r.TimeoutMs, r.MaxBodyBytes, w.maxBodyBytes)
		}
	}
}
