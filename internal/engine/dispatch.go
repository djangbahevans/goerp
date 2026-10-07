package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/rs/zerolog/log"
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

var slugParamPattern = regexp.MustCompile(tenant.SlugPattern)

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
			httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
			return
		}

		if paramName, ok := validatePathParams(rr.entry.Manifest.PathParams, rr.pathParams); !ok {
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_path_param", fmt.Sprintf("path parameter %q does not match its declared kind", paramName))
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
			if tenantCtx.Entitlements.ModuleDisabledByTenant(rr.entry.ModuleName) {
				httperr.Write(r.Context(), w, http.StatusNotFound, "route_not_found", "No route matches this path")
				return
			}
			httperr.WriteDetails(r.Context(), w, http.StatusForbidden, "billing.module_not_available", "module is not available on the current plan", map[string]any{
				"module":      rr.entry.ModuleName,
				"upgrade_url": "/settings/billing/upgrade",
			})
			return
		}

		mod, ok := rr.snap.Modules()[rr.entry.ModuleName]
		if !ok || mod.Status != module.StatusReady {
			httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "module_unavailable", "module is not ready")
			return
		}

		maxBody := rr.entry.Manifest.MaxBodyBytes
		if maxBody <= 0 {
			maxBody = defaultMaxBodyBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)

		// Native CRUD routes bypass WASM; buffering gives both paths the
		// same error-envelope annotation through writeResponse.
		if rr.entry.Manifest.EngineNative {
			rec := newEngineResponseRecorder()
			e.dispatchORMRoute(rec, r)
			writeResponse(ctx, w, rec.EngineResponse())
			return
		}

		e.dispatchWASMRoute(ctx, w, r, rr, mod)
	})
}

// dispatchWASMRoute borrows a module instance and invokes its handler for
// any route that isn't EngineNative.
func (e *Engine) dispatchWASMRoute(ctx context.Context, w http.ResponseWriter, r *http.Request, rr *routeResolution, mod *module.LoadedModule) {
	entry := rr.entry

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		// The only way this read fails is the MaxBytesReader limit set
		// just before this call.
		httperr.Write(r.Context(), w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
		return
	}

	authCtx := authFromContext(ctx)
	tenantCtx := tenantFromContext(ctx)
	if authCtx == nil || tenantCtx == nil {
		// Unreachable through the middleware chain; guards direct calls in tests.
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "pool_exhausted", fmt.Sprintf("module %s is at capacity", entry.ModuleName))
		return
	}
	defer mod.Pool.Return(inst)

	// The module's router matches its routes as declared, without the
	// module-name prefix the engine's RouteTable adds.
	modulePath := moduleRelativePath(r.URL.EscapedPath(), route.ModulePathPrefix(entry.ModuleName, mod.Manifest.Type))

	req := EngineRequest{
		ID:            requestIDFromContext(ctx),
		Method:        r.Method,
		Path:          modulePath,
		PathParams:    rr.pathParams,
		QueryParams:   r.URL.Query(),
		Headers:       moduleRequestHeaders(r.Header),
		Body:          bodyBytes,
		UserID:        authCtx.UserID,
		TenantID:      tenantCtx.TenantID,
		TenantSlug:    tenantCtx.Slug,
		TraceID:       httperr.TraceIDFromContext(ctx),
		RequestedAt:   time.Now(),
		PermissionSet: authCtx.PermissionSet,
		ContactID:     authCtx.ContactID,
		RolesLive:     authCtx.RolesLive,
	}

	if entry.Manifest.Name != "" {
		req.Model, req.Action = entry.Manifest.Model, entry.Manifest.Name
	}

	resp, err := e.invokeHandler(ctx, inst, entry.PathTemplate, req, mod)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			timeout := entry.Manifest.Timeout
			if timeout <= 0 {
				timeout = defaultHandlerTimeout
			}
			httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "computation_limit_exceeded", fmt.Sprintf("handler exceeded its %s timeout", timeout))
			return
		}
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "dispatch_error", err.Error())
		return
	}

	writeResponse(ctx, w, resp)
}

// moduleRequestHeaders keys every request header value by its lowercase
// name, the form engine.Request.Header looks names up in. It drops the
// caller's credentials: the engine has already authenticated the request,
// and module code must not be able to read or replay them.
func moduleRequestHeaders(h http.Header) map[string][]string {
	headers := make(map[string][]string, len(h))
	for name, values := range h {
		key := strings.ToLower(name)
		switch key {
		case "authorization", "proxy-authorization", "cookie":
			continue
		}
		headers[key] = append(headers[key], values...)
	}
	return headers
}

// moduleRelativePath drops prefix's segments from the front of a matched
// request's escaped path, keeping the remainder percent-encoded so the
// module's router splits it the way RouteTable.Lookup did: an encoded %2F
// inside a parameter stays one segment. The prefix is removed by segment
// count rather than by string match because Lookup also accepted the
// prefix if it was percent-encoded or contained duplicate slashes.
func moduleRelativePath(escapedPath, prefix string) string {
	skip := strings.Count(prefix, "/")
	var rest []string
	for segment := range strings.SplitSeq(escapedPath, "/") {
		if segment == "" {
			continue
		}
		if skip > 0 {
			skip--
			continue
		}
		rest = append(rest, segment)
	}
	return "/" + strings.Join(rest, "/")
}

// writeResponse is the one place either dispatch path — dispatchORMRoute
// (via engineResponseRecorder, for EngineNative routes) or invokeHandler
// (for WASM-backed routes) — writes an EngineResponse to the wire, so both
// produce a byte-identical envelope through one function rather than two
// independently-maintained copies. Error bodies get the request's ids here.
func writeResponse(ctx context.Context, w http.ResponseWriter, resp EngineResponse) {
	body := resp.Body
	if resp.StatusCode >= http.StatusBadRequest {
		body = httperr.Annotate(ctx, body)
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if len(body) != len(resp.Body) {
		w.Header().Del("Content-Length")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(body); err != nil {
		log.Error().Err(err).Msg("dispatch: write response")
	}
}

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
