package engine

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/route"
)

// verifyBuiltinRouteParity fails hard if any key in builtins has no
// matching registration in table — the goerp#822 bug class: a handler
// dispatched from builtinRoutes but missing from
// registry.registerBuiltinRoutes 404s in routeResolutionMiddleware before
// dispatch is ever reached, and nothing else catches that.
func verifyBuiltinRouteParity(builtins map[string]http.Handler, table *route.RouteTable) error {
	for key := range builtins {
		method, path, ok := strings.Cut(key, " ")
		if !ok {
			return fmt.Errorf("builtin route key %q is not \"METHOD /path\"", key)
		}
		if _, _, result, _ := table.Lookup(method, path); result != route.RouteFound {
			return fmt.Errorf("builtin route %s is dispatched from builtinRoutes but missing from registerBuiltinRoutes (registry.go) — it would 404 before dispatch", key)
		}
	}
	return nil
}
