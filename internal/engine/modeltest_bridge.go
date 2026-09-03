package engine

import (
	"context"
	"net/http"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// NewModuleTestHandler builds the route-resolution -> auth-required gate
// -> dispatch chain (buildChain's own stages, minus the ones that need
// live infrastructure a single-module test harness never stands up: rate
// limiting, tenant/auth resolution from a real session, MFA enforcement,
// hot reload, tracing) for sdk/go/modeltest, which resolves its own
// tenant/auth directly from harness state and injects them via
// WithTenantContext/WithAuthContext below rather than through a real
// login flow. rt is the same *wasm.Runtime the harness compiled and
// loaded its one module into — invokeHandler needs it for the module
// context's transaction limiter.
func NewModuleTestHandler(reg *registry.ModuleRegistry, rt *wasm.Runtime) http.Handler {
	e := &Engine{moduleRegistry: reg, wasmRuntime: rt}
	h := e.buildDispatchHandler(nil)
	h = routeAuthMiddleware()(h)
	h = routeResolutionMiddleware(reg)(h)
	return h
}

// WithTenantContext stashes tc on ctx exactly as tenantResolutionMiddleware
// does in the real chain, for sdk/go/modeltest to inject its own harness
// tenant instead of resolving one from a live request.
func WithTenantContext(ctx context.Context, tc *tenantresolve.TenantContext) context.Context {
	return withTenantContext(ctx, tc)
}

// WithAuthContext stashes ac on ctx exactly as authMiddleware does in the
// real chain, for sdk/go/modeltest to inject its own harness user instead
// of resolving one from a real session/JWT.
func WithAuthContext(ctx context.Context, ac *authcheck.AuthContext) context.Context {
	return withAuthContext(ctx, ac)
}
