package wasm

import (
	"context"
	"database/sql"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/vmihailenco/msgpack/v5"
)

// A nested call needs a fresh instance because WASM cannot reenter the caller.
// It inherits request identity while using the target's capabilities and declarations.
// The caller must defer the returned cleanup function.
func borrowModuleInstance(ctx context.Context, r *Runtime, modCtx *ModuleContext, moduleName string) (inst *ModuleInstance, cleanup func(), hostErr *abiv1.HostError) {
	target, ok := modCtx.ComputeTargets()[moduleName]
	if !ok || target.Pool == nil {
		return nil, nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "module " + moduleName + " is not available"}
	}

	inst, err := target.Pool.Borrow(ctx)
	if err != nil {
		return nil, nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	depCtx := NewModuleContext(
		modCtx.RequestID, moduleName, modCtx.UserID, modCtx.ContactID, modCtx.Roles, modCtx.PermissionSet,
		modCtx.TenantID, modCtx.TenantSlug, modCtx.TraceID, target.Capabilities, modCtx.txLimiter,
		ModuleSnapshot{
			ModelDecls:          target.ModelDecls,
			FieldSecRegistry:    modCtx.FieldSecRegistry(),
			EventRegistry:       modCtx.EventRegistry(),
			ComputedIndex:       modCtx.ComputedIndex(),
			ComputeTargets:      modCtx.ComputeTargets(),
			ConfigSchema:        target.ConfigSchema,
			UsesConfig:          target.UsesConfig,
			JobTypes:            target.JobTypes,
			HTTPAllowlist:       target.HTTPAllowlist,
			ORMBulkMaxRows:      modCtx.ormBulkMaxRows(),
			ORMStatementTimeout: modCtx.ormStatementTimeout(),
		},
	)
	inst.SetModuleContext(depCtx)
	r.RegisterInstance(inst)

	return inst, func() {
		r.UnregisterInstance(inst)
		depCtx.RollbackAll()
		inst.SetModuleContext(nil)
		target.Pool.Return(inst)
	}, nil
}

// invokeCompute borrows a fresh instance of dep's owning module and
// invokes its registered compute function against record, returning the
// recomputed value. A non-nil tx is the write transaction the recompute
// belongs to: the function's ORM reads run inside it and see the write
// that triggered the recompute.
func invokeCompute(ctx context.Context, r *Runtime, modCtx *ModuleContext, tx *sql.Tx, dep computed.Dependent, record map[string]any) (any, *abiv1.HostError) {
	inst, cleanup, hostErr := borrowModuleInstance(ctx, r, modCtx, dep.ModuleName)
	if hostErr != nil {
		return nil, hostErr
	}
	defer cleanup()
	inst.ModuleContext().readTx = tx

	payload, err := msgpack.Marshal(abiv1.ComputeRequest{
		FnName:   dep.ComputeFn,
		Record:   record,
		TenantID: modCtx.TenantID,
		UserID:   modCtx.UserID,
		TraceID:  modCtx.TraceID,
	})
	if err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
	}

	respBytes, err := inst.InvokeHandleComputed(ctx, payload)
	if err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "compute " + dep.Field + ": " + err.Error()}
	}

	var resp abiv1.ComputeResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
	}
	if resp.Error != nil {
		return nil, &abiv1.HostError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	return resp.Value, nil
}
