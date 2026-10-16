package engine

import (
	"net/http"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"go.opentelemetry.io/otel/trace"
)

func buildChain(e *Engine, reg *registry.ModuleRegistry, builtins map[string]http.Handler, trustedProxies []string, tenantResolver *tenantresolve.Resolver, authChecker *authcheck.Checker, tracer trace.Tracer, redisClient *cache.Client, defaultRateLimit route.RateLimitConfig) http.Handler {
	chain := []func(http.Handler) http.Handler{
		e.metrics.middleware,
		recoveryMiddleware(),
		requestIDMiddleware(),
		realIPMiddleware(trustedProxies),
		routeResolutionMiddleware(reg),
		otelMiddleware(tracer),
		rateLimitMiddleware(redisClient, defaultRateLimit),
		tenantResolutionMiddleware(tenantResolver),
		authMiddleware(authChecker),
		mfaEnforcementMiddleware(authChecker),
		routeAuthMiddleware(),
	}

	var handler = e.buildDispatchHandler(builtins)
	for _, c := range slices.Backward(chain) {
		handler = c(handler)
	}
	return handler
}
