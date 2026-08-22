package engine

import (
	"context"
	"errors"
	"net/http"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

type tenantContextKey struct{}

func withTenantContext(ctx context.Context, tc *tenantresolve.TenantContext) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tc)
}

// tenantFromContext returns the TenantContext tenantResolutionMiddleware
// resolved for this request, or nil for an EngineNative route (that
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
// this request, or nil for an EngineNative route (that middleware's own
// no-op case) or a request that hasn't reached that middleware yet.
func authFromContext(ctx context.Context) *authcheck.AuthContext {
	ac, _ := ctx.Value(authContextKey{}).(*authcheck.AuthContext)
	return ac
}

// tenantResolutionMiddleware implements auth-internals.md §9 step 5's
// Class A path — Host-header tenant resolution — for every module route.
// Engine-builtin routes (EngineNative) are a deliberate no-op here: each
// one already resolves its own tenant directly inside its own handler
// (the "Class-A-direct-primitives" pattern established across the MFA
// cluster of tickets, adopted because this middleware didn't exist yet
// when they were built), and several of them (e.g. /auth/login) are
// Class B — a different tenant source entirely (a request-body field),
// which only that route's own handler knows how to apply. Nothing about
// those existing handlers needs to change now that this middleware
// exists; the doc comments on mfareverify/mfareset/loginflow/mfaverify
// already say as much.
func tenantResolutionMiddleware(resolver *tenantresolve.Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr == nil || rr.entry.Manifest.EngineNative {
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
					writeRouteError(w, http.StatusNotFound, "not_found", "not found")
				case errors.Is(err, tenantresolve.ErrTenantSuspended):
					writeRouteError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
				default:
					writeRouteError(w, http.StatusInternalServerError, "internal_error", "tenant resolution failed")
				}
				return
			}

			next.ServeHTTP(w, r.WithContext(withTenantContext(r.Context(), tenantCtx)))
		})
	}
}

// authMiddleware implements auth-internals.md §9 steps 6-8 and (via
// Checker.Authenticate's own requiredPermissions check) step 10/11's
// permission half, for every module route — token extraction, JWT/erp_
// API-key/anonymous validation, user/tenant-membership hydration, and
// permission-set hydration, all already fused into
// authcheck.Checker.Authenticate rather than reimplemented here.
//
// The mfa_token third branch (Checker.AuthenticateMFAToken) is
// deliberately not wired into this middleware: auth-internals.md §9's own
// "Route classes" section scopes that branch to /auth/mfa/verify only,
// and that route is EngineNative — handled entirely by its own handler,
// which already extracts and verifies the mfa_token itself from the POST
// body (predating this middleware). Teeing every module route's request
// body here on the chance it contains an mfa_token would cost every
// ordinary request a buffered body read for a branch that, today, no
// route reaches through this middleware at all. AuthenticateMFAToken
// remains available, tested, and ready for whichever future Class A
// route actually needs this specific path (a possible follow-up once one
// exists), consistent with goerp#224's own scope note that nothing built
// the direct-primitives way needs unwinding when this middleware landed.
//
// A deliberate no-op for EngineNative routes, same reasoning as
// tenantResolutionMiddleware.
func authMiddleware(checker *authcheck.Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr == nil || rr.entry.Manifest.EngineNative {
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
				writeRouteError(w, status, code, "authentication failed")
				return
			}

			next.ServeHTTP(w, r.WithContext(withAuthContext(r.Context(), authCtx)))
		})
	}
}

// authenticateErrorResponse maps a Checker.Authenticate error to a status
// code and error code. auth-internals.md §9 step 11 is the only step
// documented with a distinct status for one particular cause ("permissions
// declared and user lacks them: 403") — every other rejection (invalid/
// expired/blocklisted token, inactive user, non-member) collapses to a
// single generic 401, matching the convention this codebase's existing
// direct-primitives handlers (mfareverify) already use rather than
// leaking which specific check failed.
func authenticateErrorResponse(err error) (int, string) {
	if errors.Is(err, authcheck.ErrPermissionDenied) {
		return http.StatusForbidden, "permission_denied"
	}
	return http.StatusUnauthorized, "unauthenticated"
}

// mfaEnforcementMiddleware implements auth-internals.md §9 step 9 for
// every module route reached by an authenticated JWT session. Anonymous
// requests and API-key-authenticated ones are passed through unevaluated
// — MFA assurance (AMR, MFAVerifiedAt) is a session concept neither of
// those carries, and an Anonymous request to a route requiring auth is
// routeAuthMiddleware's rejection to make, not this step's. A deliberate
// no-op for EngineNative routes, same reasoning as
// tenantResolutionMiddleware — /auth/mfa/verify, /auth/mfa/reverify, and
// /auth/mfa/enroll* are exempt from this check per auth-internals.md §8
// precisely because they're EngineNative and never reach here.
func mfaEnforcementMiddleware(checker *authcheck.Checker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr == nil || rr.entry.Manifest.EngineNative {
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
				writeRouteError(w, http.StatusInternalServerError, "internal_error", "mfa enforcement check failed")
				return
			}
			if decision != enforce.Allowed {
				writeRouteError(w, http.StatusForbidden, string(decision), "mfa enforcement required")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// routeAuthMiddleware implements auth-internals.md §9 step 11's residual
// scope once permission checking has already happened inside
// Checker.Authenticate (folded there rather than duplicated here, see
// authMiddleware's own doc comment): reject a request to a route
// declaring RouteManifest.Auth == "required" when the resolved
// AuthContext isn't a full authenticated session — this is the "If
// auth=Required and auth=Anonymous: 401" line from the pipeline diagram.
// MFAPending is deliberately treated the same as Anonymous here (the
// diagram's own AuthContext doc: "route authorization treats this the
// same as Anonymous for every other route, since only IsAuthenticated
// gates them") — not applicable in practice today since no module route
// authenticates via AuthenticateMFAToken (see authMiddleware), but kept
// correct for when one does. A deliberate no-op for EngineNative routes,
// same reasoning as tenantResolutionMiddleware.
func routeAuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr == nil || rr.entry.Manifest.EngineNative {
				next.ServeHTTP(w, r)
				return
			}

			if rr.entry.Manifest.Auth == "required" {
				authCtx := authFromContext(r.Context())
				if authCtx == nil || !authCtx.IsAuthenticated {
					writeRouteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
