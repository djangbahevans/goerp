package engine

import (
	"net/http"
	"runtime/debug"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/rs/zerolog/log"
)

// recoveryMiddleware logs downstream Go panics and returns 500 without exposing stack
// traces. WASM traps are handled separately by dispatch.
func recoveryMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Tracked so the 500 below still carries the request_id/
			// trace_id set by middleware running inside this one.
			r = r.WithContext(httperr.TrackIDs(r.Context()))
			defer func() {
				if rec := recover(); rec != nil {
					log.Error().
						Interface("panic", rec).
						Bytes("stack", debug.Stack()).
						Str("method", r.Method).
						Str("path", r.URL.Path).
						Msg("engine: recovered panic in request handling")
					httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
