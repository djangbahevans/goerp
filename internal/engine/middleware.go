package engine

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// routeResolution is the result of routeResolutionMiddleware's single
// early Snapshot()/Lookup() call — engine-internals.md §6: "Route
// resolution happens once, early in the chain — not inside
// dispatchHandler." Every middleware and the terminal dispatch handler
// below it reads this from context instead of re-resolving, so a second
// registry reload landing mid-chain can never make two stages disagree
// on which route/snapshot matched.
type routeResolution struct {
	snap           *registry.RegistrySnapshot
	entry          *route.RouteEntry
	pathParams     map[string]string
	allowedMethods []string
}

type routeResolutionContextKey struct{}

func withRouteResolution(ctx context.Context, rr *routeResolution) context.Context {
	return context.WithValue(ctx, routeResolutionContextKey{}, rr)
}

// routeResolutionFromContext returns the routeResolution stashed by
// routeResolutionMiddleware. Every handler reachable past it in buildChain
// is guaranteed one — routeResolutionMiddleware runs first and always
// stashes a value before calling next, or short-circuits before next is
// ever invoked.
func routeResolutionFromContext(ctx context.Context) *routeResolution {
	rr, _ := ctx.Value(routeResolutionContextKey{}).(*routeResolution)
	return rr
}

// routeResolutionMiddleware performs the request's only
// registry.Snapshot()/RouteTable.Lookup() call. RouteNotFound/
// RouteBadPath/RouteMethodNotAllowed short-circuit here, before rate
// limiting, tenant resolution, or auth ever run.
func routeResolutionMiddleware(reg *registry.ModuleRegistry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			snap := reg.Snapshot()
			if snap == nil {
				writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
				return
			}

			entry, params, result, allowedMethods := snap.RouteTable().Lookup(r.Method, r.URL.Path)
			switch result {
			case route.RouteNotFound, route.RouteBadPath:
				writeRouteError(w, http.StatusNotFound, "route_not_found", "No route matches this path")
				return
			case route.RouteMethodNotAllowed:
				w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
				writeRouteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "This path does not support "+r.Method)
				return
			}

			ctx := withRouteResolution(r.Context(), &routeResolution{
				snap:           snap,
				entry:          entry,
				pathParams:     params,
				allowedMethods: allowedMethods,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requestIDHeader matches Go's canonical MIME header casing regardless of
// how it's written here (net/http canonicalizes it on both Set and Get).
const requestIDHeader = "X-Request-Id"

type requestIDContextKey struct{}

// requestIDFromContext returns the id requestIDMiddleware minted for this
// request, or "" if the middleware hasn't run (e.g. a direct unit test of
// a handler in isolation).
func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	return id
}

// requestIDMiddleware assigns a fresh UUIDv7 request id to every request
// — engine-internals.md §6 step 2 — echoed in the response header and
// stashed in context for every later middleware/handler's log lines to
// correlate against. Always minted fresh rather than trusting an inbound
// X-Request-Id: honoring a client-supplied id would let one client's
// requests collide with (or spoof) another's in the engine's own logs.
func requestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := uuid.NewV7()
			if err != nil {
				// crypto/rand exhaustion is effectively unreachable in
				// practice; falling back to v4 keeps the request moving
				// with a still-unique, just non-time-ordered id rather
				// than failing the request over a logging concern.
				id = uuid.New()
			}
			idStr := id.String()
			w.Header().Set(requestIDHeader, idStr)
			ctx := context.WithValue(r.Context(), requestIDContextKey{}, idStr)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// realIPMiddleware resolves the client's real IP — engine-internals.md §6
// step 3 — honoring X-Forwarded-For/X-Real-IP only when the immediate TCP
// peer (r.RemoteAddr) is itself a trusted proxy; otherwise an untrusted
// client's own forwarded-for header is never believed, since it would
// otherwise let any client spoof its apparent IP for rate limiting or
// audit logging. The resolved IP is written back into r.RemoteAddr (bare
// IP, no port) so every existing remoteIP-consuming call site
// (authcheck.Checker.authenticateAPIKey's ipAllowed check, rate limiting)
// sees it without needing its own context plumbing.
func realIPMiddleware(trustedProxies []string) func(http.Handler) http.Handler {
	trusted := make([]*net.IPNet, 0, len(trustedProxies))
	for _, p := range trustedProxies {
		if _, cidr, err := net.ParseCIDR(p); err == nil {
			trusted = append(trusted, cidr)
			continue
		}
		if ip := net.ParseIP(p); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			trusted = append(trusted, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		log.Warn().Str("entry", p).Msg("engine: unparseable GOERP_TRUSTED_PROXIES entry, ignoring")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				peer = r.RemoteAddr
			}

			if realIP := resolveRealIP(peer, r, trusted); realIP != "" {
				r.RemoteAddr = realIP
			} else {
				r.RemoteAddr = peer
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolveRealIP returns the client IP to trust for this request, or ""
// if peer isn't a trusted proxy (the caller falls back to the raw peer
// address in that case). X-Forwarded-For is a comma-separated list added
// to by each hop; the first entry is the original client, per the
// standard convention.
func resolveRealIP(peer string, r *http.Request, trusted []*net.IPNet) string {
	if !ipTrusted(peer, trusted) {
		return ""
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if ip := net.ParseIP(first); ip != nil {
			return first
		}
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		if ip := net.ParseIP(strings.TrimSpace(xrip)); ip != nil {
			return xrip
		}
	}
	return ""
}

func ipTrusted(peer string, trusted []*net.IPNet) bool {
	ip := net.ParseIP(peer)
	if ip == nil {
		return false
	}
	for _, cidr := range trusted {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}
