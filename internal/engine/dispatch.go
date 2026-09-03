package engine

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

// defaultHandlerTimeout is the wall-clock budget for a route's handler
// invocation when RouteManifest.Timeout is unset — engine-internals.md's
// own documented default ("engine.Timeout(d time.Duration) ... default
// 30s if the module didn't declare one"), and, per that same doc, "the
// only execution-control limit the engine has at all."
const defaultHandlerTimeout = 30 * time.Second

// defaultMaxBodyBytes is the request body cap used when a route's
// RouteManifest.MaxBodyBytes is unset (0) — route.RegisterModelRoutes
// never sets one on EnableOps-derived routes, so falling back to a literal
// 0-byte MaxBytesReader limit would reject every EnableOps create/update
// request outright. Mirrors defaultHandlerTimeout's own "manifest didn't
// declare one" fallback pattern.
const defaultMaxBodyBytes = 1 << 20 // 1 MiB

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
func (e *Engine) buildDispatchHandler(builtins map[string]http.Handler) http.Handler {
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

		// EngineBuiltin (goerp#369) routes always live in the builtins
		// map. So does a module-less EngineNative route (goerp#417) —
		// e.g. GET /_meta/permissions, which has no owning module to
		// look up below and would otherwise 503 as module_unavailable.
		// EnableOps CRUD routes (goerp#366) are also EngineNative but do
		// have a ModuleName, so they fall through to the module lookup
		// and dispatchORMRoute branch below as before.
		if rr.entry.Manifest.EngineBuiltin || rr.entry.ModuleName == "" {
			if h, ok := builtins[r.Method+" "+rr.entry.PathTemplate]; ok {
				r = r.WithContext(route.WithParams(r.Context(), rr.pathParams))
				h.ServeHTTP(w, r)
				return
			}
		}

		// Entitlement-based dispatch gating (multitenancy-internals.md §8
		// "Entitlement-based disabling", goerp#441) — a single in-memory
		// map lookup against the EntitlementSet already loaded once at
		// tenant resolution, no extra query. Checked before the module
		// lookup below so an un-entitled tenant always sees the same 403
		// regardless of the module's own load state, rather than a
		// different error (503 module_unavailable) leaking that the
		// module exists but merely isn't ready yet. tenantCtx is nil only
		// when this handler is invoked directly, bypassing the real
		// middleware chain (tenantResolutionMiddleware always runs first
		// for any non-EngineBuiltin route, goerp#369) — skip the check
		// rather than invent a new failure mode for that test-only case;
		// downstream dispatch already has its own nil-tenantCtx guard.
		if tenantCtx := tenantFromContext(ctx); rr.entry.ModuleName != "" && tenantCtx != nil && !tenantCtx.Entitlements.ModuleEnabled(rr.entry.ModuleName) {
			writeRouteErrorDetails(w, http.StatusForbidden, "billing.module_not_available", "module is not available on the current plan", map[string]any{
				"module":      rr.entry.ModuleName,
				"upgrade_url": "/settings/billing/upgrade",
			})
			return
		}

		mod, ok := rr.snap.Modules()[rr.entry.ModuleName]
		if !ok || mod.Status != module.StatusReady {
			writeRouteError(w, http.StatusServiceUnavailable, "module_unavailable", "module is not ready")
			return
		}

		maxBody := rr.entry.Manifest.MaxBodyBytes
		if maxBody <= 0 {
			maxBody = defaultMaxBodyBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)

		// EnableOps-served routes (Table/Transient models) never borrow a
		// WASM instance — the entire point of go-sdk-reference.md's
		// "auto-generated CRUD routes" guarantee. dispatchORMRoute keeps
		// its existing signature (writes straight to a ResponseWriter,
		// unchanged since goerp#346) — recorded here so both dispatch
		// paths still end at the one shared writeResponse call below.
		if rr.entry.Manifest.EngineNative {
			rec := newEngineResponseRecorder()
			e.dispatchORMRoute(rec, r)
			writeResponse(w, rec.EngineResponse())
			return
		}

		e.dispatchWASMRoute(ctx, w, r, rr, mod)
	})
}

// dispatchWASMRoute borrows a module instance and invokes its handler for
// any route that isn't EngineNative (a hand-registered WASM route, or a
// Virtual-backend EnableOps route once goerp#373 lands — dispatchORMRoute
// itself still owns Virtual dispatch's own WASM call, not this path).
func (e *Engine) dispatchWASMRoute(ctx context.Context, w http.ResponseWriter, r *http.Request, rr *routeResolution, mod *module.LoadedModule) {
	entry := rr.entry

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		// The only way this read fails is the MaxBytesReader limit set
		// just before this call.
		writeRouteError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
		return
	}

	authCtx := authFromContext(ctx)
	tenantCtx := tenantFromContext(ctx)
	if authCtx == nil || tenantCtx == nil {
		// Unreachable via the real middleware chain — tenantResolutionMiddleware/
		// authMiddleware both run for any non-EngineBuiltin route (goerp#369)
		// before dispatchWASMRoute is ever reached. Guarded for direct-call
		// testability, matching dispatchORMRoute's own identical guard.
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		writeRouteError(w, http.StatusServiceUnavailable, "pool_exhausted", fmt.Sprintf("module %s is at capacity", entry.ModuleName))
		return
	}
	defer mod.Pool.Return(inst)

	headers := make(map[string]string, len(r.Header))
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}
	query := make(map[string]string, len(r.URL.Query()))
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}

	// The module's own SDK router matches against routes exactly as its
	// author declared them, unprefixed — a module has no way to know its
	// own manifest name at route-registration time, so the module-name
	// namespace RegisterModuleRoutes prepends for the engine's own
	// RouteTable (route.ModulePathPrefix) has to come back off before the
	// module sees this request's path, or its router can never match.
	modulePath := strings.TrimPrefix(r.URL.Path, route.ModulePathPrefix(entry.ModuleName, mod.Manifest.Type))
	if modulePath == "" {
		modulePath = "/"
	}

	req := EngineRequest{
		ID:            requestIDFromContext(ctx),
		Method:        r.Method,
		Path:          modulePath,
		PathParams:    rr.pathParams,
		QueryParams:   query,
		Headers:       headers,
		Body:          bodyBytes,
		UserID:        authCtx.UserID,
		PermissionSet: authCtx.PermissionSet,
		TenantID:      tenantCtx.TenantID,
		TenantSlug:    tenantCtx.Slug,
		TraceID:       trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		RequestAt:     time.Now(),
	}

	resp, err := e.invokeHandler(ctx, inst, entry.PathTemplate, req, mod)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			timeout := entry.Manifest.Timeout
			if timeout <= 0 {
				timeout = defaultHandlerTimeout
			}
			writeRouteError(w, http.StatusServiceUnavailable, "computation_limit_exceeded", fmt.Sprintf("handler exceeded its %s timeout", timeout))
			return
		}
		writeRouteError(w, http.StatusInternalServerError, "dispatch_error", err.Error())
		return
	}

	writeResponse(w, resp)
}

// writeResponse is the one place either dispatch path — dispatchORMRoute
// (via engineResponseRecorder, for EngineNative routes) or invokeHandler
// (for WASM-backed routes) — writes an EngineResponse to the wire, so both
// produce a byte-identical envelope through one function rather than two
// independently-maintained copies.
func writeResponse(w http.ResponseWriter, resp EngineResponse) {
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(resp.Body); err != nil {
		log.Error().Err(err).Msg("dispatch: write response")
	}
}

// engineResponseRecorder is an http.ResponseWriter that buffers what it's
// given instead of writing to the wire, so dispatchORMRoute — which writes
// directly to a ResponseWriter and predates this ticket — can still be
// funneled through writeResponse without changing its signature or
// touching its already-shipped, already-tested CRUD handlers.
type engineResponseRecorder struct {
	header     http.Header
	statusCode int
	body       bytes.Buffer
}

func newEngineResponseRecorder() *engineResponseRecorder {
	return &engineResponseRecorder{header: make(http.Header), statusCode: http.StatusOK}
}

func (r *engineResponseRecorder) Header() http.Header { return r.header }

func (r *engineResponseRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }

func (r *engineResponseRecorder) WriteHeader(statusCode int) { r.statusCode = statusCode }

func (r *engineResponseRecorder) EngineResponse() EngineResponse {
	headers := make(map[string]string, len(r.header))
	for k := range r.header {
		headers[k] = r.header.Get(k)
	}
	return EngineResponse{StatusCode: r.statusCode, Headers: headers, Body: r.body.Bytes()}
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
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func writeRouteError(w http.ResponseWriter, status int, code, message string) {
	writeRouteErrorDetails(w, status, code, message, nil)
}

// writeRouteErrorDetails is writeRouteError plus an optional details
// object — e.g. billing.module_not_available's "module"/"upgrade_url"
// fields (multitenancy-internals.md §8).
func writeRouteErrorDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// jsontext options match encoding/json v1's Encoder defaults, which
	// json.MarshalWrite doesn't apply on its own: '<', '>', '&' escaped
	// for safe HTML embedding, and U+2028/U+2029 escaped for safe JS
	// embedding.
	env := routeErrorEnvelope{Error: routeErrorBody{Code: code, Message: message, Details: details}}
	if err := json.MarshalWrite(w, env, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true)); err != nil {
		log.Error().Err(err).Msg("dispatch: encode error response")
	}
}
