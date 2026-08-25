package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

// CredentialValidator is satisfied by *workflowworker.Manager. Declared
// here rather than imported so this package doesn't need workflowworker's
// os/exec/storage/Temporal dependencies just to register this route or
// test it — a small consumer-defined interface, not the concrete type.
type CredentialValidator interface {
	// Validate reports whether token is currently live and authorized to
	// dispatch activities for moduleName.
	Validate(token, moduleName string) bool
}

// ActivityDispatchDeps are the collaborators dispatch needs to resolve a
// module's WASM pool, build its execution context, and authenticate the
// calling workflow-worker process.
type ActivityDispatchDeps struct {
	Registry    *registry.ModuleRegistry
	Tenants     *tenant.Store
	TxLimiter   *wasm.TransactionLimiter
	Credentials CredentialValidator
}

// RegisterActivityDispatchRoute wires POST /admin/_internal/activity-dispatch
// onto mux, which must be Server.UnauthenticatedRouter() (engine-internals.md
// §11 "Workflow-worker authentication").
func RegisterActivityDispatchRoute(mux *http.ServeMux, deps ActivityDispatchDeps) {
	h := &activityDispatchHandler{deps: deps}
	mux.HandleFunc("POST /admin/_internal/activity-dispatch", h.dispatch)
}

type activityDispatchHandler struct {
	deps ActivityDispatchDeps
}

// activityDispatchRequest is the JSON body a workflow-worker process POSTs
// (workflow-guide.md §2), covering every field engine.ActivityRequest needs.
type activityDispatchRequest struct {
	Module     string          `json:"module"`
	Activity   string          `json:"activity"`
	Payload    json.RawMessage `json:"payload"`
	TenantID   string          `json:"tenant_id"`
	UserID     string          `json:"user_id"`
	TraceID    string          `json:"trace_id"`
	WorkflowID string          `json:"workflow_id"`
	RunID      string          `json:"run_id"`
	Attempt    int32           `json:"attempt"`
}

// activityDispatchResult is engine.ActivityResult, JSON-transcoded for the
// wire back to workflow-worker.
type activityDispatchResult struct {
	Output       any            `json:"output,omitempty"`
	Error        string         `json:"error,omitempty"`
	NonRetryable bool           `json:"non_retryable"`
	ErrorType    string         `json:"error_type,omitempty"`
	ErrorDetails map[string]any `json:"error_details,omitempty"`
}

// dispatch resolves the module's WASM pool and invokes handle_activity. A
// transport-level failure is a non-2xx envelope error; a dispatched
// activity's own success or business error is always 200, folded into data.
func (h *activityDispatchHandler) dispatch(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[activityDispatchRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if req.Module == "" || req.Activity == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "module and activity are required")
		return
	}

	// Checked before the registry lookup below, not after: an
	// unauthenticated caller shouldn't be able to distinguish "wrong
	// credential" from "module doesn't exist" (engine-internals.md §11
	// "Workflow-worker authentication").
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !h.deps.Credentials.Validate(bearer, req.Module) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid workflow-worker credential")
		return
	}

	snap := h.deps.Registry.Snapshot()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}
	mod, ok := snap.Modules()[req.Module]
	if !ok {
		writeError(w, http.StatusNotFound, "module_not_found", "no loaded module named "+req.Module)
		return
	}
	// A module that failed to load (bad manifest/checksum) is still
	// published with Pool nil — guard against it rather than panic.
	if mod.Pool == nil {
		writeError(w, http.StatusServiceUnavailable, "module_unavailable", "module "+req.Module+" failed to load: "+mod.FailureReason)
		return
	}

	// ModuleContext needs the tenant slug too (host_db.go builds the
	// search path from it); the request only carries the ID.
	t, err := h.deps.Tenants.GetByID(r.Context(), req.TenantID)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			writeError(w, http.StatusNotFound, "tenant_not_found", "no tenant with id "+req.TenantID)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	var payload any
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "payload is not valid JSON")
			return
		}
	}
	msgpackPayload, err := msgpack.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "encode payload: "+err.Error())
		return
	}

	inst, err := mod.Pool.Borrow(r.Context())
	if err != nil {
		if errors.Is(err, wasm.ErrPoolDraining) {
			writeError(w, http.StatusServiceUnavailable, "module_unavailable", "module is unloading")
			return
		}
		if errors.Is(err, wasm.ErrPoolTimeout) {
			writeError(w, http.StatusServiceUnavailable, "pool_timeout", "no WASM instance became available in time")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	defer mod.Pool.Return(inst)

	moduleCtx := wasm.NewModuleContext(req.WorkflowID, mod.Manifest.Name, req.UserID, "", nil, req.TenantID, t.Slug, req.TraceID, mod.Capabilities, h.deps.TxLimiter, wasm.ModuleSnapshot{
		ModelDecls:       mod.ModelDecls,
		FieldSecRegistry: snap.FieldSecRegistry(),
		EventRegistry:    snap.EventRegistry(),
		ComputedIndex:    snap.ComputedIndex(),
		ComputeTargets:   registry.ComputeTargets(snap),
	})
	inst.SetModuleContext(moduleCtx)
	defer func() {
		moduleCtx.RollbackAll()
		inst.SetModuleContext(nil)
	}()

	activityReq := engine.ActivityRequest{
		Activity:   req.Activity,
		Payload:    msgpackPayload,
		TenantID:   req.TenantID,
		UserID:     req.UserID,
		TraceID:    req.TraceID,
		WorkflowID: req.WorkflowID,
		RunID:      req.RunID,
		Attempt:    req.Attempt,
	}
	reqBytes, err := msgpack.Marshal(&activityReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "encode activity request: "+err.Error())
		return
	}

	respBytes, err := inst.InvokeHandleActivity(r.Context(), reqBytes)
	if err != nil {
		if ctxErr := r.Context().Err(); ctxErr != nil {
			writeError(w, http.StatusGatewayTimeout, "activity_timeout", ctxErr.Error())
			return
		}
		log.Error().Err(err).Str("module", req.Module).Str("activity", req.Activity).Msg("activity-dispatch: handle_activity trapped")
		writeError(w, http.StatusInternalServerError, "activity_trapped", "activity handler trapped")
		return
	}

	var result engine.ActivityResult
	if err := msgpack.Unmarshal(respBytes, &result); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "decode activity result: "+err.Error())
		return
	}

	out := activityDispatchResult{
		Error:        result.Error,
		NonRetryable: result.NonRetryable,
		ErrorType:    result.ErrorType,
		ErrorDetails: result.ErrorDetails,
	}
	if len(result.Output) > 0 {
		var output any
		if err := msgpack.Unmarshal(result.Output, &output); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "decode activity output: "+err.Error())
			return
		}
		out.Output = output
	}

	writeData(w, http.StatusOK, out)
}
