package engine

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/rs/zerolog/log"
)

// buildDispatchHandler is the engine's single HTTP router: every request,
// built-in and module route alike, resolves through the same
// registry.ModuleRegistry snapshot's RouteTable — no second router
// anywhere in the request path. It is buildChain's only stage for now;
// auth/tenant/rate-limit/otel middleware (engine-internals.md §6's full
// buildChain) land in later tickets once their own prerequisites (a real
// EngineResponse, tenant/auth pipelines) exist.
func buildDispatchHandler(reg *registry.ModuleRegistry, builtins map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snap := reg.Snapshot()
		if snap == nil {
			writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
			return
		}

		entry, _, result, allowedMethods := snap.RouteTable().Lookup(r.Method, r.URL.Path)
		switch result {
		case route.RouteNotFound, route.RouteBadPath:
			writeRouteError(w, http.StatusNotFound, "route_not_found", "No route matches this path")
			return
		case route.RouteMethodNotAllowed:
			w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
			writeRouteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "This path does not support "+r.Method)
			return
		}

		if entry.Manifest.EngineNative {
			if h, ok := builtins[r.Method+" "+entry.PathTemplate]; ok {
				h.ServeHTTP(w, r)
				return
			}
		}

		// Module-route dispatch to invokeHandler needs a populated
		// EngineResponse and an auth/tenant pipeline that don't exist
		// yet — an explicit 501 rather than a silent no-op or a faked
		// success.
		writeRouteError(w, http.StatusNotImplemented, "dispatch_not_implemented", "module route dispatch is not yet implemented")
	})
}

type routeErrorEnvelope struct {
	Error routeErrorBody `json:"error"`
}

type routeErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeRouteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(routeErrorEnvelope{Error: routeErrorBody{Code: code, Message: message}}); err != nil {
		log.Error().Err(err).Msg("dispatch: encode error response")
	}
}
