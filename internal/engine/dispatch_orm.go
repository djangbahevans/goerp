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

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

const (
	defaultListLimit = 50
	maxListLimit     = 100
)

// dispatchORMRoute is the HTTP-side entry point for any EnableOps-derived
// Table/Transient route — resolves the model and runs the matching
// host.orm pipeline function with zero WASM instance calls.
// buildDispatchHandler records its output and passes it through
// writeResponse, the same as a WASM handler's.
func (e *Engine) dispatchORMRoute(w http.ResponseWriter, r *http.Request) {
	rr := routeResolutionFromContext(r.Context())
	if rr == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}
	entry := rr.entry

	_, mod, md, ok := rr.snap.ModelByName(entry.Manifest.Model)
	if !ok {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "route names an unresolvable model")
		return
	}

	if entry.Manifest.StorageBackend == "virtual" {
		httperr.Write(r.Context(), w, http.StatusNotImplemented, "not_implemented", "Virtual-backed EnableOps routes are not yet served (goerp#373)")
		return
	}

	if entry.Manifest.StorageBackend == "transient" && e.cacheClient == nil {
		httperr.Write(r.Context(), w, http.StatusNotImplemented, "not_implemented", "Transient-backed routes need a cache client, which this engine was built without")
		return
	}

	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	traceID := httperr.TraceIDFromContext(r.Context())
	req := EngineRequest{
		ID:            requestIDFromContext(r.Context()),
		UserID:        authCtx.UserID,
		TenantID:      tenantCtx.TenantID,
		TenantSlug:    tenantCtx.Slug,
		TraceID:       traceID,
		PermissionSet: authCtx.PermissionSet,
		ContactID:     authCtx.ContactID,
		RolesLive:     authCtx.RolesLive,
	}
	modCtx := e.newModuleContext(r.Context(), req, mod)
	defer modCtx.RollbackAll()

	ctx := r.Context()
	insertClient := e.wasmRuntime.EventInsertClient()

	switch entry.Manifest.CrudAction {
	case "list":
		e.dispatchORMList(ctx, w, r, entry, modCtx)
	case "get":
		e.dispatchORMGet(ctx, w, r, rr.pathParams, entry, modCtx, md)
	case "create":
		e.dispatchORMCreate(ctx, w, r, entry, modCtx, md, insertClient)
	case "update":
		e.dispatchORMUpdate(ctx, w, r, rr.pathParams, entry, modCtx, md, insertClient)
	case "delete":
		e.dispatchORMDelete(ctx, w, rr.pathParams, entry, modCtx, insertClient)
	case "preview":
		e.dispatchORMPreview(ctx, w, r, entry, modCtx, md)
	case "pivot":
		e.dispatchORMPivot(ctx, w, r, entry, modCtx)
	case "workflow_transition":
		e.dispatchORMWorkflowTransition(ctx, w, rr.pathParams, entry, modCtx, md, insertClient)
	default:
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "unknown crud action: "+entry.Manifest.CrudAction)
	}
}

func (e *Engine) dispatchORMList(ctx context.Context, w http.ResponseWriter, r *http.Request, entry *route.RouteEntry, modCtx *wasm.ModuleContext) {
	q := r.URL.Query()

	order := ""
	if sort := q.Get("sort"); sort != "" {
		// ORM ordering supports one field, so retain the first field from a multi-field
		// sort request.
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
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "route names an unresolvable model")
		return
	}
	domainExpr, hostErr := compileListFilter(q, entry.Manifest.Model, md)
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	out, hostErr := wasm.ORMSearchRead(ctx, e.primaryDB, modCtx, abiv1.ORMSearchReadInput{
		Model:  entry.Manifest.Model,
		Domain: domainExpr,
		Fields: fields,
		Order:  order,
		Limit:  limit,
		Cursor: q.Get("cursor"),
	})
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	if wantsParquet(r) {
		writeParquet(ctx, w, out.Records, out.NextCursor)
		return
	}

	writeJSON(ctx, w, http.StatusOK, map[string]any{
		"data": ormRecordsToJSON(md, out.Records),
		"meta": map[string]any{
			"cursor":   out.NextCursor,
			"has_more": out.NextCursor != "",
			"total":    nil,
		},
	})
}

// dispatchORMPivot handles a Pivot-enabled model's GET {resource}/pivot
// route (view-system.md §8 "use_wasm: false"): ?rows=a,b&columns=c&
// values=field:agg,... plus the same filter[...] query params dispatchORMList
// accepts, reusing compileListFilter as-is. The actual GROUP BY ROLLUP
// aggregation is wasm.ORMPivot's job — this only parses the wire format.
func (e *Engine) dispatchORMPivot(ctx context.Context, w http.ResponseWriter, r *http.Request, entry *route.RouteEntry, modCtx *wasm.ModuleContext) {
	q := r.URL.Query()

	rows := splitNonEmpty(q.Get("rows"))
	columns := splitNonEmpty(q.Get("columns"))

	values, hostErr := parsePivotValues(q.Get("values"))
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	md, ok := resolveModelDecl(modCtx, entry.Manifest.Model)
	if !ok {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "route names an unresolvable model")
		return
	}
	domainExpr, hostErr := compileListFilter(q, entry.Manifest.Model, md)
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	out, hostErr := wasm.ORMPivot(ctx, e.primaryDB, modCtx, wasm.ORMPivotInput{
		Model:   entry.Manifest.Model,
		Domain:  domainExpr,
		Rows:    rows,
		Columns: columns,
		Values:  values,
	})
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	writeJSON(ctx, w, http.StatusOK, map[string]any{"cells": out.Cells})
}

// splitNonEmpty splits a comma-separated query param into its fields,
// returning nil (not [""]) for an absent/empty param.
func splitNonEmpty(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// parsePivotValues parses ?values=field:agg,field:agg,... (view-system.md
// §8) into structured pairs — malformed syntax (no ":", or an empty
// field/aggregation) is orm.validation_failed, the same code ORMPivot
// uses for an unrecognized aggregation name, since both are caller input
// errors rather than a specific-field lookup failure.
func parsePivotValues(raw string) ([]wasm.PivotValue, *abiv1.HostError) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	values := make([]wasm.PivotValue, 0, len(parts))
	for _, part := range parts {
		field, agg, ok := strings.Cut(part, ":")
		if !ok || field == "" || agg == "" {
			return nil, &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "values entry " + part + " must be field:aggregation"}
		}
		values = append(values, wasm.PivotValue{Field: field, Aggregation: agg})
	}
	return values, nil
}

func (e *Engine) dispatchORMGet(ctx context.Context, w http.ResponseWriter, r *http.Request, pathParams map[string]string, entry *route.RouteEntry, modCtx *wasm.ModuleContext, md model.ModelDeclaration) {
	id := pathParams["id"]
	if id == "" {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}

	out, hostErr := wasm.ORMRead(ctx, e.primaryDB, e.cacheClient, modCtx, abiv1.ORMReadInput{
		Model: entry.Manifest.Model,
		IDs:   []string{id},
	})
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}
	if len(out.Records) == 0 {
		httperr.Write(r.Context(), w, http.StatusNotFound, abiv1.ErrCodeNotFound, "record not found")
		return
	}

	writeJSON(ctx, w, http.StatusOK, ormRecordToJSON(md, out.Records[0]))
}

// Preview recomputes draft fields and runs the preview hook without persistence or record
// events.
func (e *Engine) dispatchORMPreview(ctx context.Context, w http.ResponseWriter, r *http.Request, entry *route.RouteEntry, modCtx *wasm.ModuleContext, md model.ModelDeclaration) {
	record, ok := decodeORMRecord(w, r, md)
	if !ok {
		return
	}

	out, hostErr := wasm.ORMPreview(ctx, e.wasmRuntime, modCtx, wasm.ORMPreviewInput{
		Model:  entry.Manifest.Model,
		Record: record,
	})
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	writeJSON(ctx, w, http.StatusOK, ormRecordToJSON(md, out.Record))
}

func (e *Engine) dispatchORMCreate(ctx context.Context, w http.ResponseWriter, r *http.Request, entry *route.RouteEntry, modCtx *wasm.ModuleContext, md model.ModelDeclaration, insertClient *river.Client[*sql.Tx]) {
	record, ok := decodeORMRecord(w, r, md)
	if !ok {
		return
	}

	out, hostErr := wasm.ORMCreate(ctx, e.wasmRuntime, e.primaryDB, insertClient, e.cacheClient, modCtx, abiv1.ORMCreateInput{
		Model:  entry.Manifest.Model,
		Record: record,
	})
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	writeJSON(ctx, w, http.StatusCreated, ormRecordToJSON(md, out.Record))
}

func (e *Engine) dispatchORMUpdate(ctx context.Context, w http.ResponseWriter, r *http.Request, pathParams map[string]string, entry *route.RouteEntry, modCtx *wasm.ModuleContext, md model.ModelDeclaration, insertClient *river.Client[*sql.Tx]) {
	id := pathParams["id"]
	if id == "" {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}

	record, ok := decodeORMRecord(w, r, md)
	if !ok {
		return
	}

	var expectedEtag *string
	if values, present := r.Header["If-Match"]; present && len(values) > 0 {
		expectedEtag = new(values[0])
	}

	out, hostErr := wasm.ORMWrite(ctx, e.wasmRuntime, e.primaryDB, insertClient, e.cacheClient, modCtx, abiv1.ORMWriteInput{
		Model:        entry.Manifest.Model,
		ID:           id,
		Record:       record,
		ExpectedEtag: expectedEtag,
	})
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	writeJSON(ctx, w, http.StatusOK, ormRecordToJSON(md, out.Record))
}

// Workflow transitions use an etag precondition to prevent concurrent state changes from
// overwriting one another. A pointer preserves an empty etag as a real precondition;
// Condition is not evaluated here.
func (e *Engine) dispatchORMWorkflowTransition(ctx context.Context, w http.ResponseWriter, pathParams map[string]string, entry *route.RouteEntry, modCtx *wasm.ModuleContext, md model.ModelDeclaration, insertClient *river.Client[*sql.Tx]) {
	id := pathParams["id"]
	record, ok := e.workflowTransitionRecord(ctx, w, id, entry, modCtx)
	if !ok {
		return
	}
	etag, _ := record["etag"].(string)
	wf := entry.Manifest.Workflow

	writeOut, hostErr := wasm.ORMWriteAndEmit(ctx, e.wasmRuntime, e.primaryDB, insertClient, e.cacheClient, modCtx, abiv1.ORMWriteInput{
		Model:        entry.Manifest.Model,
		ID:           id,
		Record:       map[string]any{wf.Field: wf.To},
		ExpectedEtag: new(etag),
	}, wf.Event)
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	writeJSON(ctx, w, http.StatusOK, ormRecordToJSON(md, writeOut.Record))
}

func (e *Engine) workflowTransitionRecord(ctx context.Context, w http.ResponseWriter, id string, entry *route.RouteEntry, modCtx *wasm.ModuleContext) (map[string]any, bool) {
	if id == "" {
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return nil, false
	}

	wf := entry.Manifest.Workflow

	// The state-gate read bypasses field masking and includes all fields so models without
	// etag remain readable. Passing any returned etag to the write prevents concurrent
	// transition overwrites.
	readOut, hostErr := wasm.ORMRead(ctx, e.primaryDB, e.cacheClient, modCtx, abiv1.ORMReadInput{
		Model: entry.Manifest.Model,
		IDs:   []string{id},
	}, wasm.SkipFieldSecurity())
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return nil, false
	}
	if len(readOut.Records) == 0 {
		httperr.Write(ctx, w, http.StatusNotFound, abiv1.ErrCodeNotFound, "record not found")
		return nil, false
	}

	record := readOut.Records[0]
	current, _ := record[wf.Field].(string)
	if current != wf.From {
		httperr.Write(ctx, w, http.StatusConflict, abiv1.ErrCodeInvalidTransition,
			entry.Manifest.Name+" requires "+wf.Field+" to be "+wf.From+", but it is "+current)
		return nil, false
	}

	return record, true
}

func (e *Engine) dispatchORMDelete(ctx context.Context, w http.ResponseWriter, pathParams map[string]string, entry *route.RouteEntry, modCtx *wasm.ModuleContext, insertClient *river.Client[*sql.Tx]) {
	id := pathParams["id"]
	if id == "" {
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}

	_, hostErr := wasm.ORMUnlink(ctx, e.wasmRuntime, e.primaryDB, insertClient, e.cacheClient, modCtx, abiv1.ORMUnlinkInput{
		Model: entry.Manifest.Model,
		IDs:   []string{id},
	})
	if hostErr != nil {
		writeHostError(ctx, w, hostErr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// decodeJSONRecord distinguishes body-limit failures (413) from malformed JSON (400).
func decodeJSONRecord(w http.ResponseWriter, r *http.Request) (record map[string]any, ok bool) {
	if err := json.UnmarshalRead(r.Body, &record); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			httperr.Write(r.Context(), w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
			return nil, false
		}
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_body", "request body must be a JSON object")
		return nil, false
	}
	return record, true
}

// decodeORMRecord is decodeJSONRecord plus ormRecordFromJSON's per-kind
// conversion, answering a value it can't convert with 400 invalid_body.
func decodeORMRecord(w http.ResponseWriter, r *http.Request, md model.ModelDeclaration) (map[string]any, bool) {
	record, ok := decodeJSONRecord(w, r)
	if !ok {
		return nil, false
	}
	converted, err := ormRecordFromJSON(md, record)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_body", err.Error())
		return nil, false
	}
	return converted, true
}

// writeJSON marshals to a buffer before writing anything to w — unlike
// json.MarshalWrite straight to w, a marshal failure partway through a
// large body (e.g. an ORM list response, easily over MarshalWrite's own
// ~4KB flush threshold) can't leave a truncated body behind an
// already-committed status code. The jsontext options match encoding/json
// v1's Encoder defaults, which json.Marshal doesn't apply on its own:
// '<', '>', '&' escaped for safe HTML embedding, and U+2028/U+2029
// escaped for safe JS embedding.
func writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		log.Error().Err(err).Msg("dispatchORMRoute: encode response")
		httperr.Write(ctx, w, http.StatusInternalServerError, "internal", "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(encoded); err != nil {
		log.Error().Err(err).Msg("dispatchORMRoute: write response")
	}
}

// writeHostError translates a host.orm *abiv1.HostError into the same
// {"error": {"code","message"}} envelope httperr.Write already
// produces, so an ORM-dispatched failure looks identical, over HTTP, to
// any other route error.
func writeHostError(ctx context.Context, w http.ResponseWriter, hostErr *abiv1.HostError) {
	httperr.Write(ctx, w, ormErrorStatus(hostErr.Code), hostErr.Code, hostErr.Message)
}

func ormErrorStatus(code string) int {
	switch code {
	case abiv1.ErrCodeNotFound, abiv1.ErrCodeModelNotFound:
		return http.StatusNotFound
	case abiv1.ErrCodeEtagMismatch, abiv1.ErrCodeUniqueViolation, abiv1.ErrCodeForeignKeyViolation, abiv1.ErrCodeInvalidTransition:
		return http.StatusConflict
	case abiv1.ErrCodeValidationFailed, abiv1.ErrCodeFieldUnknown, abiv1.ErrCodeFieldNotWritable, abiv1.ErrCodeDomainInvalid, abiv1.ErrCodeTransientNotListable:
		return http.StatusBadRequest
	case abiv1.ErrCodeCapabilityDenied, abiv1.ErrCodeFieldWriteDenied, abiv1.ErrCodeFieldReadDenied:
		return http.StatusForbidden
	case abiv1.ErrCodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
