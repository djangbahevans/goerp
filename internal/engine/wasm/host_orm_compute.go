package wasm

import (
	"cmp"
	"context"
	"database/sql"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/vmihailenco/msgpack/v5"
)

// A nested call needs a fresh instance because WASM cannot reenter the caller.
// It inherits request identity while using the target's capabilities and declarations.
// The caller must defer the returned cleanup function.
func borrowModuleInstance(ctx context.Context, r *Runtime, modCtx *ModuleContext, moduleName string, readTx *sql.Tx, unmaskedReads bool) (inst *ModuleInstance, cleanup func(), hostErr *abiv1.HostError) {
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
			PermissionRegistry:  modCtx.PermissionRegistry(),
			SearchIndexRegistry: modCtx.SearchIndexRegistry(),
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
	depCtx.readTx = readTx
	depCtx.unmaskedReads = unmaskedReads
	inst.SetModuleContext(depCtx)
	r.RegisterInstance(inst)

	return inst, func() {
		r.UnregisterInstance(inst)
		depCtx.RollbackAll()
		inst.SetModuleContext(nil)
		target.Pool.Return(inst)
	}, nil
}

// computeSession is one borrowed instance of dep's owning module, reused for
// every record a recompute pass computes so the borrow and module context
// are paid once rather than per record.
type computeSession struct {
	inst    *ModuleInstance
	modCtx  *ModuleContext
	dep     computed.Dependent
	cleanup func()
}

// openComputeSession borrows an instance for dep's compute function. A
// non-nil tx is the write transaction the recompute belongs to: the
// function's ORM, search and db.query reads join it and see the triggering
// write. The caller must close the session.
func openComputeSession(ctx context.Context, r *Runtime, modCtx *ModuleContext, tx *sql.Tx, dep computed.Dependent) (*computeSession, *abiv1.HostError) {
	inst, cleanup, hostErr := borrowModuleInstance(ctx, r, modCtx, cmp.Or(dep.FunctionModule, dep.ModuleName), tx, true)
	if hostErr != nil {
		return nil, hostErr
	}
	return &computeSession{inst: inst, modCtx: modCtx, dep: dep, cleanup: cleanup}, nil
}

func (s *computeSession) close() { s.cleanup() }

// invoke runs the compute function against record and returns the
// recomputed value. Transactions the function leaves open are rolled back, so
// one record's state does not reach the next.
func (s *computeSession) invoke(ctx context.Context, record map[string]any) (any, *abiv1.HostError) {
	defer s.inst.ModuleContext().RollbackAll()
	payload, err := msgpack.Marshal(abiv1.ComputeRequest{
		FnName:   s.dep.ComputeFn,
		Record:   record,
		TenantID: s.modCtx.TenantID,
		UserID:   s.modCtx.UserID,
		TraceID:  s.modCtx.TraceID,
	})
	if err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
	}

	respBytes, err := s.inst.InvokeHandleComputed(ctx, payload)
	if err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "compute " + s.dep.Field + ": " + err.Error()}
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

// invokeCompute computes one record in a session of its own.
func invokeCompute(ctx context.Context, r *Runtime, modCtx *ModuleContext, tx *sql.Tx, dep computed.Dependent, record map[string]any) (any, *abiv1.HostError) {
	session, hostErr := openComputeSession(ctx, r, modCtx, tx, dep)
	if hostErr != nil {
		return nil, hostErr
	}
	defer session.close()
	return session.invoke(ctx, record)
}
