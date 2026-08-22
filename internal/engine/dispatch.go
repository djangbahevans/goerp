package engine

import (
	"encoding/json"
	"net/http"

	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/rs/zerolog/log"
)

// buildDispatchHandler is buildChain's terminal handler. Route resolution
// already happened once, early in the chain (routeResolutionMiddleware) —
// this handler only reads the stashed result, never re-resolves, per
// engine-internals.md §6's "Route resolution happens once, early in the
// chain — not inside dispatchHandler."
func buildDispatchHandler(builtins map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr := routeResolutionFromContext(r.Context())
		if rr == nil {
			// Only reachable if buildDispatchHandler is invoked outside
			// buildChain (e.g. a misconfigured test) — routeResolutionMiddleware
			// always stashes a value before calling next in production.
			writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
			return
		}

		if rr.entry.Manifest.EngineNative {
			if h, ok := builtins[r.Method+" "+rr.entry.PathTemplate]; ok {
				r = r.WithContext(route.WithParams(r.Context(), rr.pathParams))
				h.ServeHTTP(w, r)
				return
			}
		}

		// Module-route dispatch to invokeHandler needs a populated
		// EngineResponse (goerp#92, still unbuilt) — an explicit 501
		// rather than a silent no-op or a faked success. Auth/tenant
		// context is already populated on r.Context() by the middleware
		// chain above by the time #92 lands and needs to read it.
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
