package wasm

import (
	"context"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/vmihailenco/msgpack/v5"
)

// constraintRequest/constraintResponse mirror sdk/go/orm's own
// unexported constraintRequest/constraintResponse wire shapes by field
// name and msgpack tag (sdk/go/orm/constraint.go) — the same
// cross-boundary mirroring computeRequest/computeResponse
// (host_orm_compute.go) and previewRequest/previewResponse
// (host_orm_preview.go) use for the identical reason: sdk/go/orm compiles
// into a module's WASM binary, not the engine.
type constraintRequest struct {
	Model    string         `msgpack:"model"`
	Phase    string         `msgpack:"phase"`
	Record   map[string]any `msgpack:"record"`
	TenantID string         `msgpack:"tenant_id,omitempty"`
	UserID   string         `msgpack:"user_id,omitempty"`
	TraceID  string         `msgpack:"trace_id,omitempty"`
}

type constraintResponse struct {
	Allowed bool             `msgpack:"allowed"`
	Field   string           `msgpack:"field,omitempty"`
	Message string           `msgpack:"message,omitempty"`
	Error   *constraintError `msgpack:"error,omitempty"`
}

type constraintError struct {
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}

// runConstraintHook invokes the constraint hook registered for
// (qualifiedModel, phase) against record, if the model's owning module
// has one — go-sdk-reference.md §22 "Constraint hooks". phase is
// "create"/"write"/"delete" (mirroring sdk/go/orm.ConstraintPhase's own
// wire values). A module with no live pool or that never registered any
// constraint hook is left untouched — Pool availability and
// HasHandleConstraint are both checked before ever borrowing an
// instance, the same graceful no-op runPreviewHook (host_orm_preview.go)
// already established for "no hook registered" being the expected common
// case, not a failure.
func runConstraintHook(ctx context.Context, r *Runtime, modCtx *ModuleContext, qualifiedModel, phase string, record map[string]any) *abi.HostError {
	target, ok := modCtx.ComputeTargets()[modCtx.ModuleName]
	if !ok || target.Pool == nil {
		return nil
	}

	inst, cleanup, hostErr := borrowModuleInstance(ctx, r, modCtx, modCtx.ModuleName)
	if hostErr != nil {
		return hostErr
	}
	defer cleanup()

	if !inst.HasHandleConstraint() {
		return nil
	}

	payload, err := msgpack.Marshal(constraintRequest{
		Model:    qualifiedModel,
		Phase:    phase,
		Record:   record,
		TenantID: modCtx.TenantID,
		UserID:   modCtx.UserID,
		TraceID:  modCtx.TraceID,
	})
	if err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	respBytes, err := inst.InvokeHandleConstraint(ctx, payload)
	if err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "constraint " + qualifiedModel + " " + phase + ": " + err.Error()}
	}

	var resp constraintResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if resp.Error != nil {
		return &abi.HostError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	if !resp.Allowed {
		return &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: resp.Message, Details: map[string]any{"field": resp.Field}}
	}
	return nil
}
