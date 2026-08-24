package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// defaultHandlerTimeout is the wall-clock budget for a route's handler
// invocation when RouteManifest.Timeout is unset — engine-internals.md's
// own documented default ("engine.Timeout(d time.Duration) ... default
// 30s if the module didn't declare one"), and, per that same doc, "the
// only execution-control limit the engine has at all."
const defaultHandlerTimeout = 30 * time.Second

// Path parameter kinds — go-sdk-reference.md's engine.UUIDParam/
// SlugParam/IntParam wire values, RouteManifest.PathParams' own map
// values.
const (
	pathParamKindUUID = "uuid"
	pathParamKindSlug = "slug"
	pathParamKindInt  = "int"
)

// slugParamPattern mirrors system.tenants.slug's own CHECK constraint
// (internal/engine/tenant/store.go) — the one slug-shape convention this
// codebase already establishes anywhere, reused here rather than
// inventing a second, possibly-inconsistent one for PathParam's own
// "slug" kind, which no doc pins down independently.
var slugParamPattern = regexp.MustCompile(`^[a-z][a-z0-9\-]{1,62}[a-z0-9]$`)

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

		if paramName, ok := validatePathParams(rr.entry.Manifest.PathParams, rr.pathParams); !ok {
			writeRouteError(w, http.StatusBadRequest, "invalid_path_param", fmt.Sprintf("path parameter %q does not match its declared kind", paramName))
			return
		}

		timeout := rr.entry.Manifest.Timeout
		if timeout <= 0 {
			timeout = defaultHandlerTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		r = r.WithContext(ctx)

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
		// context, and now the timeout-bounded context above, are
		// already populated on r.Context() by the time #92 lands and
		// needs to read them.
		writeRouteError(w, http.StatusNotImplemented, "dispatch_not_implemented", "module route dispatch is not yet implemented")
	})
}

// validatePathParams reports the first path param (if any) whose
// extracted value doesn't match its RouteManifest-declared kind — ok is
// false in that case, with name identifying which one failed. A param
// present in values with no corresponding entry in kinds (or vice versa)
// isn't itself a failure: kinds only names the subset of extracted
// params a route author chose to constrain, matching
// RouteManifest.PathParams' own "param name -> declared kind" semantics.
// An unrecognized kind string can't be validated and passes through
// rather than rejecting a request over a declaration this function
// doesn't understand.
func validatePathParams(kinds, values map[string]string) (name string, ok bool) {
	for paramName, kind := range kinds {
		value, present := values[paramName]
		if !present {
			continue
		}

		var valid bool
		switch kind {
		case pathParamKindUUID:
			_, err := uuid.Parse(value)
			valid = err == nil
		case pathParamKindSlug:
			valid = slugParamPattern.MatchString(value)
		case pathParamKindInt:
			_, err := strconv.Atoi(value)
			valid = err == nil
		default:
			valid = true
		}

		if !valid {
			return paramName, false
		}
	}
	return "", true
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
