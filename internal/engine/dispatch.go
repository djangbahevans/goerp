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

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/rs/zerolog/log"
)

const defaultHandlerTimeout = 30 * time.Second

// EnableOps routes leave MaxBodyBytes unset; a zero-byte limit would reject their writes.
const defaultMaxBodyBytes = 1 << 20

const (
	pathParamKindUUID = "uuid"
	pathParamKindSlug = "slug"
	pathParamKindInt  = "int"
)

var slugParamPattern = regexp.MustCompile(tenant.SlugPattern)

func (e *Engine) buildDispatchHandler(builtins map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr := routeResolutionFromContext(r.Context())
		if rr == nil {
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

		if rr.entry.Manifest.EngineBuiltin || rr.entry.ModuleName == "" {
			if h, ok := builtins[r.Method+" "+rr.entry.PathTemplate]; ok {
				r = r.WithContext(route.WithParams(r.Context(), rr.pathParams))
				h.ServeHTTP(w, r)
				return
			}
		}

		// Check entitlements before module readiness so an unauthorized caller always
		// receives 403 without learning the module's load state.
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

		e.dispatchModuleRoute(ctx, w, r, rr, mod)
	})
}

func (e *Engine) dispatchModuleRoute(ctx context.Context, w http.ResponseWriter, r *http.Request, rr *routeResolution, mod *module.LoadedModule) {
	entry := rr.entry

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
		return
	}

	authCtx := authFromContext(ctx)
	tenantCtx := tenantFromContext(ctx)
	if authCtx == nil || tenantCtx == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	// The module's router matches its routes as declared, without the
	// module-name prefix the engine's RouteTable adds.
	modulePath := moduleRelativePath(r.URL.EscapedPath(), route.ModulePathPrefix(entry.ModuleName, mod.Manifest.Type))

	requestedAt := time.Now()
	if e.wasmRuntime != nil {
		requestedAt = e.wasmRuntime.Now()
	}

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
		RequestedAt:   requestedAt,
		PermissionSet: authCtx.PermissionSet,
		ContactID:     authCtx.ContactID,
		RolesLive:     authCtx.RolesLive,
	}

	if entry.Manifest.Name != "" {
		req.Model, req.Action = entry.Manifest.Model, entry.Manifest.Name
	}

	info, err := e.wasmRuntime.ResolveLocale(ctx, req.TenantID, req.UserID)
	if err != nil {
		log.Error().Err(err).Msg("resolve invocation locale")
		httperr.Write(ctx, w, http.StatusInternalServerError, "dispatch_error", "locale resolution failed")
		return
	}
	req.Locale, req.Timezone, req.Currency, req.Direction = info.Locale, info.Timezone, info.Currency, info.Direction

	req, err = e.beforeRequestHooks(ctx, rr.snap, req)
	if err != nil {
		log.Error().Err(err).Msg("before-request hook failed")
		httperr.Write(ctx, w, http.StatusInternalServerError, "hook_error", "request hook failed")
		return
	}

	ctx = wasm.WithInvocationLocale(ctx, req.TenantID, req.UserID, abiv1.LocaleInfo{
		Locale:    req.Locale,
		Timezone:  req.Timezone,
		Currency:  req.Currency,
		Direction: req.Direction,
	})
	r = r.WithContext(ctx)

	var resp EngineResponse
	if entry.Manifest.EngineNative {
		r.Body = io.NopCloser(bytes.NewReader(req.Body))
		r.URL.RawQuery = req.QueryParams.Encode()
		r.Header = make(http.Header)
		for name, values := range req.Headers {
			r.Header[http.CanonicalHeaderKey(name)] = values
		}
		rec := newEngineResponseRecorder()
		e.dispatchORMRoute(rec, r)
		resp = rec.EngineResponse()
	} else {
		resp, err = e.invokeModuleRequest(ctx, entry.PathTemplate, req, mod)
	}
	if err == nil {
		resp, err = e.afterRequestHooks(ctx, rr.snap, req, resp)
		if err != nil {
			log.Error().Err(err).Msg("after-request hook failed")
			httperr.Write(ctx, w, http.StatusInternalServerError, "hook_error", "response hook failed")
			return
		}
	}
	if err != nil {
		if errors.Is(err, wasm.ErrPoolTimeout) || errors.Is(err, wasm.ErrPoolDraining) {
			httperr.Write(ctx, w, http.StatusServiceUnavailable, "pool_exhausted", fmt.Sprintf("module %s is at capacity", entry.ModuleName))
			return
		}
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

// Module prefix removal counts normalized segments because routing accepts escaped
// prefixes and duplicate slashes. Remaining segments retain escapes such as %2F.
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

// validatePathParams reports the first value violating a declared kind. Undeclared
// parameters and unrecognized kinds pass through.
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
