package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
)

func newTestWASMPool(t *testing.T, wasmBytes []byte, cfg wasm.PoolConfig) *wasm.InstancePool {
	t.Helper()
	ctx := t.Context()

	rt, err := wasm.New(&config.Config{
		Environment:       string(config.Production),
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		PoolMaxMemoryByes: 64 << 20,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	pool := wasm.NewInstancePool("testmod", compiled, rt, cfg)
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 10*time.Millisecond) })
	return pool
}

func TestDispatchHandler_WASMPoolFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		draining bool
		code     string
	}{
		{name: "capacity", code: "service_unavailable"},
		{name: "draining", draining: true, code: "module_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestWASMPool(t, handleRequestEchoModule, wasm.PoolConfig{
				MaxSize:       1,
				WarmSize:      1,
				BorrowTimeout: 50 * time.Millisecond,
			})
			if tc.draining {
				if err := pool.DrainAndClose(t.Context(), time.Second); err != nil {
					t.Fatal(err)
				}
			} else {
				inst, err := pool.Borrow(t.Context())
				if err != nil {
					t.Fatalf("hold pool instance: %v", err)
				}
				t.Cleanup(func() { pool.Return(inst) })
			}

			mods := map[string]*module.LoadedModule{
				"widgets": {
					Status:   module.StatusReady,
					Manifest: manifest.Manifest{Name: "widgets", Type: "standard"},
					Pool:     pool,
				},
			}
			w := dispatchTestModule(t, &Engine{}, mods, 0)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body: %s", w.Code, w.Body.String())
			}
			assertRouteErrorCode(t, w, tc.code)
		})
	}
}

func dispatchTestModule(t *testing.T, e *Engine, mods map[string]*module.LoadedModule, timeout time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(mods); err != nil {
		t.Fatalf("registry Update: %v", err)
	}
	e.moduleRegistry = reg

	entry := &route.RouteEntry{
		ModuleName:   "widgets",
		PathTemplate: "/widgets/items",
		Manifest:     route.RouteManifest{Auth: "required", Timeout: timeout},
	}
	rr := &routeResolution{snap: reg.Snapshot(), entry: entry}
	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	ctx := withRouteResolution(t.Context(), rr)
	ctx = httperr.WithRequestID(ctx, "dispatch-request")
	ctx = withTenantContext(ctx, &tenantresolve.TenantContext{
		TenantID:     "t1",
		Slug:         "acme",
		Entitlements: tenantresolve.EntitlementSet{Features: map[string]bool{"module.widgets": true}},
	})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "u1"})

	w := httptest.NewRecorder()
	e.buildDispatchHandler(nil).ServeHTTP(w, req.WithContext(ctx))
	return w
}
