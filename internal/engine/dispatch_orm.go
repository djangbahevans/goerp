package engine

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

// defaultListLimit/maxListLimit bound an unbounded ?limit= query param —
// erp-design.md §11.4 documents cursor pagination but no explicit default/
// cap, so these are new, conservative choices rather than a documented
// convention.
const (
	defaultListLimit = 50
	maxListLimit     = 100
)

// dispatchORMRoute is the HTTP-side entry point for any EnableOps-derived
// Table/Transient route (goerp#346) — resolves the model, runs the
// matching host.orm pipeline function (goerp#367) with zero WASM instance
// calls, and writes the response directly, mirroring writeRouteError's
// existing shape rather than routing through EngineResponse/writeResponse
// (that shared-with-the-WASM-path design is goerp#92's own scope, not
// this ticket's — see the plan this shipped against). Not yet wired into
// buildDispatchHandler's actual routing (also goerp#92's job); called
// directly today, by tests and by whichever future caller replaces
// dispatch.go's "dispatch_not_implemented" stub.
func (e *Engine) dispatchORMRoute(w http.ResponseWriter, r *http.Request) {
	rr := routeResolutionFromContext(r.Context())
	if rr == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}
	entry := rr.entry

	_, mod, _, ok := rr.snap.ModelByName(entry.Manifest.Model)
	if !ok {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "route names an unresolvable model")
		return
	}

	if entry.Manifest.StorageBackend == "virtual" {
		writeRouteError(w, http.StatusNotImplemented, "not_implemented", "Virtual-backed EnableOps routes are not yet served (goerp#373)")
		return
	}

	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		// Unreachable via the real middleware chain — tenantResolutionMiddleware/
		// authMiddleware both run for any EngineNative-but-not-EngineBuiltin
		// route (goerp#369) before dispatchORMRoute is ever reached. Guarded
		// for direct-call testability, matching buildDispatchHandler's own
		// rr==nil guard above.
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	traceID := trace.SpanFromContext(r.Context()).SpanContext().TraceID().String()
	req := EngineRequest{
		ID:            requestIDFromContext(r.Context()),
		UserID:        authCtx.UserID,
		PermissionSet: authCtx.PermissionSet,
		TenantID:      tenantCtx.TenantID,
		TenantSlug:    tenantCtx.Slug,
		TraceID:       traceID,
	}
	modCtx := e.newModuleContext(r.Context(), req, mod)
	defer modCtx.RollbackAll()

	ctx := r.Context()
	insertClient := e.wasmRuntime.EventInsertClient()

	switch entry.Manifest.CrudAction {
	case "list":
		e.dispatchORMList(ctx, w, r, entry, modCtx)
	case "get":
		e.dispatchORMGet(ctx, w, r, rr.pathParams, entry, modCtx)
	case "create":
		e.dispatchORMCreate(ctx, w, r, entry, modCtx, insertClient)
	case "update":
		e.dispatchORMUpdate(ctx, w, r, rr.pathParams, entry, modCtx, insertClient)
	case "delete":
		e.dispatchORMDelete(ctx, w, rr.pathParams, entry, modCtx, insertClient)
	case "preview":
		e.dispatchORMPreview(ctx, w, r, entry, modCtx)
	default:
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "unknown crud action: "+entry.Manifest.CrudAction)
	}
}

func (e *Engine) dispatchORMList(ctx context.Context, w http.ResponseWriter, r *http.Request, entry *route.RouteEntry, modCtx *wasm.ModuleContext) {
	q := r.URL.Query()

	order := ""
	if sort := q.Get("sort"); sort != "" {
		// erp-design.md §11.4 documents multi-field sort
		// ("-created_at,customer_name"); ORMSearchReadInput.Order is a
		// single field (host_orm.go's orderByExpr has no multi-field
		// support) — take the first field only rather than silently
		// dropping the request or erroring on a documented convention
		// the underlying pipeline can't fully honor yet.
		field, _, _ := strings.Cut(sort, ",")
		if after, ok := strings.CutPrefix(field, "-"); ok {
			order = after + " DESC"
		} else {
			order = field
		}
	}

	var fields []string
	for key, values := range q {
		if strings.HasPrefix(key, "fields[") && strings.HasSuffix(key, "]") && len(values) > 0 && values[0] != "" {
			fields = strings.Split(values[0], ",")
			break
		}
	}

	limit := defaultListLimit
	if raw := q.Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = min(parsed, maxListLimit)
		}
	}

	md, ok := resolveModelDecl(modCtx, entry.Manifest.Model)
	if !ok {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "route names an unresolvable model")
		return
	}
	domainExpr, hostErr := compileListFilter(q, entry.Manifest.Model, md)
	if hostErr != nil {
		writeHostError(w, hostErr)
		return
	}

	out, hostErr := wasm.ORMSearchRead(ctx, e.primaryDB, modCtx, wasm.ORMSearchReadInput{
		Model:  entry.Manifest.Model,
		Domain: domainExpr,
		Fields: fields,
		Order:  order,
		Limit:  limit,
		Cursor: q.Get("cursor"),
	})
	if hostErr != nil {
		writeHostError(w, hostErr)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": out.Records,
		"meta": map[string]any{
			"cursor":   out.NextCursor,
			"has_more": out.NextCursor != "",
			"total":    nil,
		},
	})
}

func (e *Engine) dispatchORMGet(ctx context.Context, w http.ResponseWriter, r *http.Request, pathParams map[string]string, entry *route.RouteEntry, modCtx *wasm.ModuleContext) {
	id := pathParams["id"]
	if id == "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}

	out, hostErr := wasm.ORMRead(ctx, e.primaryDB, e.cacheClient, modCtx, wasm.ORMReadInput{
		Model: entry.Manifest.Model,
		IDs:   []string{id},
	})
	if hostErr != nil {
		writeHostError(w, hostErr)
		return
	}
	if len(out.Records) == 0 {
		writeRouteError(w, http.StatusNotFound, abi.ErrCodeNotFound, "record not found")
		return
	}

	writeJSON(w, http.StatusOK, out.Records[0])
}

// dispatchORMPreview serves the Preview CRUD op (goerp#372) —
// wasm.ORMPreview recomputes every Store(true)/.Depends() field whose
// dependencies are present in the draft body, then runs a registered
// PreviewHook if the model's module has one. Unlike every other
// CrudAction here, this never persists anything and needs no
// insertClient — no orm.record.* event is ever emitted for a preview.
func (e *Engine) dispatchORMPreview(ctx context.Context, w http.ResponseWriter, r *http.Request, entry *route.RouteEntry, modCtx *wasm.ModuleContext) {
	record, ok := decodeJSONRecord(w, r)
	if !ok {
		return
	}

	out, hostErr := wasm.ORMPreview(ctx, e.wasmRuntime, modCtx, wasm.ORMPreviewInput{
		Model:  entry.Manifest.Model,
		Record: record,
	})
	if hostErr != nil {
		writeHostError(w, hostErr)
		return
	}

	writeJSON(w, http.StatusOK, out.Record)
}

func (e *Engine) dispatchORMCreate(ctx context.Context, w http.ResponseWriter, r *http.Request, entry *route.RouteEntry, modCtx *wasm.ModuleContext, insertClient *river.Client[*sql.Tx]) {
	record, ok := decodeJSONRecord(w, r)
	if !ok {
		return
	}

	out, hostErr := wasm.ORMCreate(ctx, e.wasmRuntime, e.primaryDB, insertClient, e.cacheClient, modCtx, wasm.ORMCreateInput{
		Model:  entry.Manifest.Model,
		Record: record,
	})
	if hostErr != nil {
		writeHostError(w, hostErr)
		return
	}

	writeJSON(w, http.StatusCreated, out.Record)
}

func (e *Engine) dispatchORMUpdate(ctx context.Context, w http.ResponseWriter, r *http.Request, pathParams map[string]string, entry *route.RouteEntry, modCtx *wasm.ModuleContext, insertClient *river.Client[*sql.Tx]) {
	id := pathParams["id"]
	if id == "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}

	record, ok := decodeJSONRecord(w, r)
	if !ok {
		return
	}

	out, hostErr := wasm.ORMWrite(ctx, e.wasmRuntime, e.primaryDB, insertClient, e.cacheClient, modCtx, wasm.ORMWriteInput{
		Model:        entry.Manifest.Model,
		ID:           id,
		Record:       record,
		ExpectedEtag: r.Header.Get("If-Match"),
	})
	if hostErr != nil {
		writeHostError(w, hostErr)
		return
	}

	writeJSON(w, http.StatusOK, out.Record)
}

func (e *Engine) dispatchORMDelete(ctx context.Context, w http.ResponseWriter, pathParams map[string]string, entry *route.RouteEntry, modCtx *wasm.ModuleContext, insertClient *river.Client[*sql.Tx]) {
	id := pathParams["id"]
	if id == "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}

	_, hostErr := wasm.ORMUnlink(ctx, e.wasmRuntime, e.primaryDB, insertClient, e.cacheClient, modCtx, wasm.ORMUnlinkInput{
		Model: entry.Manifest.Model,
		IDs:   []string{id},
	})
	if hostErr != nil {
		writeHostError(w, hostErr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// decodeJSONRecord decodes r's body into a record map for create/update.
// r.Body is wrapped in an http.MaxBytesReader by buildDispatchHandler
// before dispatchORMRoute ever runs (goerp#92) — exceeding that limit
// surfaces here as a *http.MaxBytesError, which gets its own 413 rather
// than being folded into the generic 400 a malformed body gets.
func decodeJSONRecord(w http.ResponseWriter, r *http.Request) (record map[string]any, ok bool) {
	if err := json.UnmarshalRead(r.Body, &record); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeRouteError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
			return nil, false
		}
		writeRouteError(w, http.StatusBadRequest, "invalid_body", "request body must be a JSON object")
		return nil, false
	}
	return record, true
}

// writeJSON marshals to a buffer before writing anything to w — unlike
// json.MarshalWrite straight to w, a marshal failure partway through a
// large body (e.g. an ORM list response, easily over MarshalWrite's own
// ~4KB flush threshold) can't leave a truncated body behind an
// already-committed status code. The jsontext options match encoding/json
// v1's Encoder defaults, which json.Marshal doesn't apply on its own:
// '<', '>', '&' escaped for safe HTML embedding, and U+2028/U+2029
// escaped for safe JS embedding.
func writeJSON(w http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		log.Error().Err(err).Msg("dispatchORMRoute: encode response")
		writeRouteError(w, http.StatusInternalServerError, "internal", "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(encoded); err != nil {
		log.Error().Err(err).Msg("dispatchORMRoute: write response")
	}
}

// writeHostError translates a host.orm *abi.HostError into the same
// {"error": {"code","message"}} envelope writeRouteError already
// produces, so an ORM-dispatched failure looks identical, over HTTP, to
// any other route error.
func writeHostError(w http.ResponseWriter, hostErr *abi.HostError) {
	writeRouteError(w, ormErrorStatus(hostErr.Code), hostErr.Code, hostErr.Message)
}

// ormErrorStatus maps a host.orm *abi.HostError's Code to an HTTP status.
// No such mapping exists anywhere else in the codebase yet — sdk/go/engine's
// documented FromHostError (go-sdk-reference.md) was never built either —
// this is the first real one, grounded in the two documented precedents
// that do exist: orm.not_found -> 404 and orm.etag_mismatch -> 409
// (host-abi-reference.md's own note on orm.Write's 0-row disambiguation;
// erp-design.md's status table).
func ormErrorStatus(code string) int {
	switch code {
	case abi.ErrCodeNotFound, abi.ErrCodeModelNotFound:
		return http.StatusNotFound
	case abi.ErrCodeEtagMismatch, abi.ErrCodeUniqueViolation, abi.ErrCodeForeignKeyViolation:
		return http.StatusConflict
	case abi.ErrCodeValidationFailed, abi.ErrCodeFieldUnknown, abi.ErrCodeDomainInvalid, abi.ErrCodeTransientNotListable:
		return http.StatusBadRequest
	case abi.ErrCodeCapabilityDenied, abi.ErrCodeFieldWriteDenied:
		return http.StatusForbidden
	case abi.ErrCodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
