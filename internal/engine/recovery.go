package engine

import (
	"net/http"
	"runtime/debug"

	"github.com/rs/zerolog/log"
)

// recoveryMiddleware is buildChain's outermost wrapper — engine-internals.md
// §6 step 1 — catching any panic anywhere downstream in the chain
// (including every later middleware and the terminal dispatch handler)
// and returning 500 internal_error instead of crashing the process or
// simply dropping the connection. The panic value and stack trace are
// logged, never included in the response body.
//
// A WASM module-side panic surfaces to Go as a Wazero trap, not a Go
// panic — it never reaches recover() here at all, and is already
// distinguished and handled by the dispatch error path responsible for
// wazero errors (goerp#92).
func recoveryMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error().
						Interface("panic", rec).
						Bytes("stack", debug.Stack()).
						Str("method", r.Method).
						Str("path", r.URL.Path).
						Msg("engine: recovered panic in request handling")
					writeRouteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
