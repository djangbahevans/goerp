package engine

import (
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/perm"
)

// Middleware is an option applied to every route registered on a group:
// RequireAuth, Requires, RateLimit, MaxBody, Timeout and the other route
// options. It is a declaration, not code that runs in the module: the engine
// enforces what a route declares (authentication, permissions, rate limit,
// body size, timeout) before dispatching to the handler, so a request that
// fails one is answered 401, 403 or 429 without reaching it.
type Middleware = RouteOption

// RouteGroup registers routes under a shared path prefix with shared
// middleware.
type RouteGroup struct {
	router     *Router
	prefix     string
	middleware []Middleware
}

// Group returns a group on the default router. Routes registered on it are
// declared at prefix plus their own pattern, with the middleware applied
// before each route's own options, so a route's explicit option overrides a
// group's and permissions from both accumulate.
func Group(prefix string, middleware ...Middleware) *RouteGroup {
	return DefaultRouter.Group(prefix, middleware...)
}

// Group returns a group on r; see the package-level Group.
func (r *Router) Group(prefix string, middleware ...Middleware) *RouteGroup {
	return &RouteGroup{router: r, prefix: prefix, middleware: middleware}
}

// Group returns a group nested in g: its prefix extends g's and its
// middleware follows g's.
func (g *RouteGroup) Group(prefix string, middleware ...Middleware) *RouteGroup {
	return &RouteGroup{
		router:     g.router,
		prefix:     joinPattern(g.prefix, prefix),
		middleware: slices.Concat(g.middleware, middleware),
	}
}

func (g *RouteGroup) GET(pattern string, h Handler, opts ...RouteOption) {
	g.router.register("GET", joinPattern(g.prefix, pattern), h, g.options(opts)...)
}

func (g *RouteGroup) POST(pattern string, h Handler, opts ...RouteOption) {
	g.router.register("POST", joinPattern(g.prefix, pattern), h, g.options(opts)...)
}

func (g *RouteGroup) PUT(pattern string, h Handler, opts ...RouteOption) {
	g.router.register("PUT", joinPattern(g.prefix, pattern), h, g.options(opts)...)
}

func (g *RouteGroup) PATCH(pattern string, h Handler, opts ...RouteOption) {
	g.router.register("PATCH", joinPattern(g.prefix, pattern), h, g.options(opts)...)
}

func (g *RouteGroup) DELETE(pattern string, h Handler, opts ...RouteOption) {
	g.router.register("DELETE", joinPattern(g.prefix, pattern), h, g.options(opts)...)
}

// WS registers a WebSocket-upgrade route on the group.
func (g *RouteGroup) WS(pattern string, h Handler, opts ...RouteOption) {
	g.router.registerWebsocket("GET", joinPattern(g.prefix, pattern), h, g.options(opts)...)
}

// SSE registers a server-sent-events route on the group; Streaming() is
// implied, as for the package-level SSE.
func (g *RouteGroup) SSE(pattern string, h Handler, opts ...RouteOption) {
	g.router.register("GET", joinPattern(g.prefix, pattern), h, g.options(append([]RouteOption{Streaming()}, opts...))...)
}

// options returns the group's middleware followed by opts, so that later
// options override earlier ones.
func (g *RouteGroup) options(opts []RouteOption) []RouteOption {
	return slices.Concat(g.middleware, opts)
}

// joinPattern joins a group prefix and a route pattern into one pattern. An
// empty pattern is the group's own path.
func joinPattern(prefix, pattern string) string {
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(pattern, "/")
}

// RequirePermission requires the caller to hold p on every route of a group.
// It is Requires as middleware: the permission's name is declared on each
// route, and the engine denies a request that lacks it before the handler
// runs. It panics on the zero perm.Permission, which would leave the group
// unguarded.
func RequirePermission(p perm.Permission) Middleware {
	return Requires(p)
}

// RequireAuth requires a valid session, which is already the default for a
// route; it states the requirement on a group, or restores it for a nested
// group after an option relaxed it.
func RequireAuth() Middleware {
	return Auth(AuthRequired)
}
