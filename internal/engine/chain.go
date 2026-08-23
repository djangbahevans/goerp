package engine

import (
	"net/http"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"go.opentelemetry.io/otel/trace"
)

// buildChain assembles the engine's HTTP middleware chain —
// engine-internals.md §6 — terminating in buildDispatchHandler. Built in
// reverse order: the first entry in chain is outermost (first to run).
//
// Rate limiting (goerp#329) is a separate, sibling ticket not yet wired
// in here — its slot is simply absent from this list rather than stubbed
// as a no-op, since an absent stage and a no-op stage are
// indistinguishable to every request this chain currently serves.
func buildChain(reg *registry.ModuleRegistry, builtins map[string]http.Handler, trustedProxies []string, tenantResolver *tenantresolve.Resolver, authChecker *authcheck.Checker, tracer trace.Tracer) http.Handler {
	chain := []func(http.Handler) http.Handler{
		recoveryMiddleware(),
		requestIDMiddleware(),
		realIPMiddleware(trustedProxies),
		routeResolutionMiddleware(reg),
		tenantResolutionMiddleware(tenantResolver),
		authMiddleware(authChecker),
		mfaEnforcementMiddleware(authChecker),
		routeAuthMiddleware(),
		otelMiddleware(tracer),
	}

	var handler = buildDispatchHandler(builtins)
	for _, c := range slices.Backward(chain) {
		handler = c(handler)
	}
	return handler
}
