package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
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

// The test holds the pool's only instance so dispatch must hit BorrowTimeout.
func TestDispatchHandler_WASMPoolExhaustedReturns503PoolExhausted(t *testing.T) {
	pool := newTestWASMPool(t, handleRequestEchoModule, wasm.PoolConfig{
		MaxSize: 1, WarmSize: 1, BorrowTimeout: 50 * time.Millisecond,
	})
	if _, err := pool.Borrow(t.Context()); err != nil {
		t.Fatalf("Borrow (holding instance): %v", err)
	}

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"widgets": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Name: "widgets", Type: "standard"},
			Pool:     pool,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	entry := &route.RouteEntry{
		ModuleName:   "widgets",
		PathTemplate: "/widgets/items",
		Manifest:     route.RouteManifest{Auth: "required"},
	}
	rr := &routeResolution{snap: reg.Snapshot(), entry: entry}

	e := &Engine{}
	h := e.buildDispatchHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	ctx := withRouteResolution(req.Context(), rr)
	ctx = withTenantContext(ctx, &tenantresolve.TenantContext{
		TenantID:     "t1",
		Slug:         "acme",
		Entitlements: tenantresolve.EntitlementSet{Features: map[string]bool{"module.widgets": true}},
	})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "u1"})
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
	assertRouteErrorCode(t, w, "pool_exhausted")
}
