package engine

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/route"
)

// A builtin handler missing from the route table is rejected before dispatch.
// verifyBuiltinRouteParity catches that registration mismatch at startup.
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
