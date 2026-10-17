package wasm

import (
	"context"
	"database/sql"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

// Constraints use a fresh instance to avoid reentering the caller's WASM stack.
// A module without a live pool or constraint export has no hook to invoke.
func runConstraintHook(ctx context.Context, r *Runtime, tx *sql.Tx, modCtx *ModuleContext, qualifiedModel, phase string, record map[string]any) *abiv1.HostError {
	target, ok := modCtx.ComputeTargets()[modCtx.ModuleName]
	if !ok || target.Pool == nil {
		return nil
	}

	inst, cleanup, hostErr := borrowModuleInstance(ctx, r, modCtx, modCtx.ModuleName, tx, false)
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
