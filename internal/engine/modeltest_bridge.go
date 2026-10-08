package engine

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// NewModuleTestHandler builds a module dispatch chain enforcing route authentication
// and permissions against injected harness identities. It omits live session resolution,
// rate limiting, MFA and hot reload. db and rt serve ORM and WASM requests.
func NewModuleTestHandler(reg *registry.ModuleRegistry, rt *wasm.Runtime, db *sql.DB) http.Handler {
	e := &Engine{moduleRegistry: reg, wasmRuntime: rt, primaryDB: db}
	dispatch := e.buildDispatchHandler(nil)
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr := routeResolutionFromContext(r.Context())
		authCtx := authFromContext(r.Context())
		if !rr.entry.Manifest.EngineBuiltin {
			for _, required := range rr.entry.Manifest.Permissions {
				idx, ok := rr.snap.PermissionRegistry().Index(required)
				if !ok || authCtx == nil || !authCtx.PermissionSet.Has(idx) {
					httperr.Write(r.Context(), w, http.StatusForbidden, "permission_denied", "missing required permission")
					return
				}
			}
		}

		dispatch.ServeHTTP(w, r)
	})
	h = routeAuthMiddleware()(h)
	h = routeResolutionMiddleware(reg)(h)
	h = recoveryMiddleware()(h)
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
