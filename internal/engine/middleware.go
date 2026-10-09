package engine

import (
	"context"
	"net"
	"net/http"
	"strings"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/rs/zerolog/log"
)

// A shared resolution prevents registry reloads from making middleware
// and dispatch disagree about the matched route.
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

// Every handler past route resolution in buildChain has a resolution;
// unresolved requests return before downstream middleware runs.
func routeResolutionFromContext(ctx context.Context) *routeResolution {
	rr, _ := ctx.Value(routeResolutionContextKey{}).(*routeResolution)
	return rr
}

func routeResolutionMiddleware(reg *registry.ModuleRegistry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			snap := reg.Snapshot()
			if snap == nil {
				httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
				return
			}

			// Lookup splits on literal "/" before decoding each segment, so
			// it needs the escaped path: r.URL.Path has already turned an
			// encoded %2F inside a parameter into a separator.
			entry, params, result, allowedMethods := snap.RouteTable().Lookup(r.Method, r.URL.EscapedPath())
			switch result {
			case route.RouteBadPath:
				httperr.Write(r.Context(), w, http.StatusBadRequest, "bad_path", "Invalid request path")
				return
			case route.RouteNotFound:
				httperr.Write(r.Context(), w, http.StatusNotFound, "route_not_found", "No route matches this path")
				return
			case route.RouteMethodNotAllowed:
				w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
				httperr.Write(r.Context(), w, http.StatusMethodNotAllowed, "method_not_allowed", "This path does not support "+r.Method)
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

const requestIDHeader = "X-Request-Id"

func requestIDFromContext(ctx context.Context) string {
	return httperr.RequestIDFromContext(ctx)
}

// Fresh IDs prevent client-supplied headers from spoofing another request
// in the engine's logs.
func requestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			idStr := uuid.NewV7().String()
			w.Header().Set(requestIDHeader, idStr)
			ctx := httperr.WithRequestID(r.Context(), idStr)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Forwarded IP headers are trusted only from configured proxies to prevent
// clients from spoofing their address for rate limiting or audit logs.
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
