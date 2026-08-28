package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/tetratelabs/wazero"
)

// newTestWASMPool compiles wasmBytes and builds a real *wasm.InstancePool
// on top of it — the same construction newTestInstance (engine_invoke_test.go)
// uses, factored out here so a non-EngineNative dispatch test can control
// the pool itself (borrow from it, exhaust it) rather than just borrowing
// one instance and handing it directly to invokeHandler.
func newTestWASMPool(t *testing.T, wasmBytes []byte, cfg wasm.PoolConfig) *wasm.InstancePool {
	t.Helper()
	ctx := context.Background()

	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = rt.Close(ctx) })

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	pool := wasm.NewInstancePool("testmod", compiled, rt, cfg)
	t.Cleanup(func() { pool.DrainAndClose(ctx, 10*time.Millisecond) })
	return pool
}

// TestDispatchHandler_WASMPoolExhaustedReturns503PoolExhausted proves
// buildDispatchHandler's non-EngineNative branch maps a Borrow failure
// (goerp#92's AC: "pool exhaustion returns 503 pool_exhausted") rather
// than surfacing wasm.ErrPoolTimeout as a 500 or panicking. The pool's
// only instance is borrowed and deliberately never returned, so the
// request's own Borrow call has nothing available and hits the pool's
// short BorrowTimeout.
func TestDispatchHandler_WASMPoolExhaustedReturns503PoolExhausted(t *testing.T) {
	pool := newTestWASMPool(t, handleRequestEchoModule, wasm.PoolConfig{
		MaxSize: 1, WarmSize: 1, BorrowTimeout: 50 * time.Millisecond,
	})
	if _, err := pool.Borrow(context.Background()); err != nil {
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
