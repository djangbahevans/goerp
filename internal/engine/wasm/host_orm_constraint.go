package wasm

import (
	"context"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

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
func runConstraintHook(ctx context.Context, r *Runtime, modCtx *ModuleContext, qualifiedModel, phase string, record map[string]any) *abiv1.HostError {
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

	payload, err := msgpack.Marshal(abiv1.ConstraintRequest{
		Model:    qualifiedModel,
		Phase:    phase,
		Record:   record,
		TenantID: modCtx.TenantID,
		UserID:   modCtx.UserID,
		TraceID:  modCtx.TraceID,
	})
	if err != nil {
		return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
	}

	respBytes, err := inst.InvokeHandleConstraint(ctx, payload)
	if err != nil {
		return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "constraint " + qualifiedModel + " " + phase + ": " + err.Error()}
	}

	var resp abiv1.ConstraintResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
	}
	if resp.Error != nil {
		return &abiv1.HostError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	if !resp.Allowed {
		return &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: resp.Message, Details: map[string]any{"field": resp.Field}}
	}
	return nil
}
