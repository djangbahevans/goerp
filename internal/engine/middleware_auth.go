package engine

import (
	"context"
	"errors"
	"net/http"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type tenantContextKey struct{}

func withTenantContext(ctx context.Context, tc *tenantresolve.TenantContext) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tc)
}

// tenantFromContext returns the TenantContext tenantResolutionMiddleware
// resolved for this request, or nil for an EngineBuiltin route (that
// middleware's own no-op case, documented on it) or a request that
// hasn't reached that middleware yet.
func tenantFromContext(ctx context.Context) *tenantresolve.TenantContext {
	tc, _ := ctx.Value(tenantContextKey{}).(*tenantresolve.TenantContext)
	return tc
}

type authContextKey struct{}

func withAuthContext(ctx context.Context, ac *authcheck.AuthContext) context.Context {
	return context.WithValue(ctx, authContextKey{}, ac)
}

// authFromContext returns the AuthContext authMiddleware populated for
// this request, or nil for an EngineBuiltin route (that middleware's own
// no-op case) or a request that hasn't reached that middleware yet.
func authFromContext(ctx context.Context) *authcheck.AuthContext {
	ac, _ := ctx.Value(authContextKey{}).(*authcheck.AuthContext)
	return ac
}

// tenantResolutionMiddleware resolves module requests by Host. EngineBuiltin handlers
// choose their own tenant sources; native module routes still require this middleware.
func tenantResolutionMiddleware(resolver *tenantresolve.Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr == nil || rr.entry.Manifest.EngineBuiltin {
				next.ServeHTTP(w, r)
				return
			}

			tenantCtx, err := resolver.ResolveByHost(r.Context(), r.Host)
			if err != nil {
				switch {
				case errors.Is(err, tenantresolve.ErrTenantNotFound):
					// 404, not 401/403 — multitenancy-internals.md §4:
					// never reveal via a different response whether a
					// tenant exists at all, same convention
					// mfareverify's own tenant-resolution error mapping
					// already uses.
					httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not found")
				case errors.Is(err, tenantresolve.ErrTenantSuspended):
					httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
				case errors.Is(err, tenantresolve.ErrTenantOffboarding):
					httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
				default:
					httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "tenant resolution failed")
				}
				return
			}

			trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("tenant.id", tenantCtx.TenantID))
			next.ServeHTTP(w, r.WithContext(withTenantContext(r.Context(), tenantCtx)))
		})
	}
}

// authMiddleware authenticates module requests through Checker and skips EngineBuiltin
// routes. MFA verification handlers read mfa_token from their own request bodies instead
// of buffering every module request here.
func authMiddleware(checker *authcheck.Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr == nil || rr.entry.Manifest.EngineBuiltin {
				next.ServeHTTP(w, r)
				return
			}

			// tenantResolutionMiddleware runs immediately before this one
			// in buildChain and already short-circuited the request on
			// any resolution failure — tenantCtx is always populated here.
			tenantCtx := tenantFromContext(r.Context())

			rawToken := authcheck.ExtractToken(r)
			authCtx, err := checker.Authenticate(r.Context(), rawToken, tenantCtx.TenantID, tenantCtx.Slug, r.RemoteAddr, rr.snap.PermissionRegistry(), rr.entry.Manifest.Permissions)
			if err != nil {
				status, code := authenticateErrorResponse(err)
				httperr.Write(r.Context(), w, status, code, "authentication failed")
				return
			}

			next.ServeHTTP(w, r.WithContext(withAuthContext(r.Context(), authCtx)))
		})
	}
}

// Authentication failures use a generic 401 to avoid revealing credential checks.
// Permission, membership and password-change requirements retain their specific 403
// responses.
func authenticateErrorResponse(err error) (int, string) {
	switch {
	case errors.Is(err, authcheck.ErrPermissionDenied):
		return http.StatusForbidden, "permission_denied"
	case errors.Is(err, authcheck.ErrNotTenantMember):
		return http.StatusForbidden, "tenant_membership_required"
	case errors.Is(err, authcheck.ErrPasswordChangeRequired):
		return http.StatusForbidden, "password_change_required"
	}
	return http.StatusUnauthorized, "unauthenticated"
}

// MFA enforcement applies to authenticated JWT sessions. API keys and anonymous requests
// carry no session assurance; builtin MFA handlers enforce their own rules.
func mfaEnforcementMiddleware(checker *authcheck.Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr == nil || rr.entry.Manifest.EngineBuiltin {
				next.ServeHTTP(w, r)
				return
			}

			authCtx := authFromContext(r.Context())
			if authCtx == nil || !authCtx.IsAuthenticated || authCtx.AuthMethod != "jwt" {
				next.ServeHTTP(w, r)
				return
			}

			tenantCtx := tenantFromContext(r.Context())
			decision, err := checker.EnforceMFA(r.Context(), rr.entry.PathTemplate, tenantCtx.TenantID, authCtx)
			if err != nil {
				httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa enforcement check failed")
				return
			}
			if decision != enforce.Allowed {
				httperr.Write(r.Context(), w, http.StatusForbidden, string(decision), "mfa enforcement required")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// routeAuthMiddleware requires a fully authenticated principal for required-auth routes.
// Pending MFA is insufficient; builtin handlers own their authorization.
func routeAuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr == nil || rr.entry.Manifest.EngineBuiltin {
				next.ServeHTTP(w, r)
				return
			}

			if rr.entry.Manifest.Auth == "required" {
				authCtx := authFromContext(r.Context())
				if authCtx == nil || !authCtx.IsAuthenticated {
					httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "authentication required")
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
