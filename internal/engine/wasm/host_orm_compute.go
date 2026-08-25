package wasm

import (
	"context"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/vmihailenco/msgpack/v5"
)

// This file holds the cross-module compute-function dispatch both
// host_orm_write.go (recompute after create/write) and host_orm.go
// (Store(false) fields, computed fresh on read) call into — a single
// choke point for "borrow a fresh instance of the field's owning module
// and run its .Computed() function," so the two call sites never diverge
// on how a nested ModuleContext gets built.

// computeRequest/computeResponse mirror sdk/go/orm's own computeRequest/
// computeResponse (sdk/go/orm/computed.go) field-for-field via matching
// msgpack tags — the engine-side counterpart of a wire type it can't
// import directly (sdk/go/orm compiles into a module's WASM binary, not
// the engine).
type computeRequest struct {
	FnName   string         `msgpack:"fn_name"`
	Record   map[string]any `msgpack:"record"`
	TenantID string         `msgpack:"tenant_id,omitempty"`
	UserID   string         `msgpack:"user_id,omitempty"`
	TraceID  string         `msgpack:"trace_id,omitempty"`
}

type computeResponse struct {
	Value any           `msgpack:"value,omitempty"`
	Error *computeError `msgpack:"error,omitempty"`
}

type computeError struct {
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}

// borrowModuleInstance borrows a fresh instance from moduleName's own
// pool and builds a nested ModuleContext scoped to that module's own
// capabilities/models, inheriting the triggering request's identity
// (tenant/user/trace) and registries from modCtx — so a nested call's own
// host.orm calls (e.g. a compute function or preview hook looking up
// related data) resolve exactly as if this were a normal request to that
// module. Always a new instance — never the currently-executing one,
// which can't be reentered mid-call — uniform whether moduleName is the
// module that triggered the call or a different one reached through a
// Many2One-hop dependency (go-sdk-reference.md §22 "Computed field
// recomputation"). The returned cleanup func must be deferred by the
// caller; hostErr is non-nil only when inst/cleanup are both nil.
func borrowModuleInstance(ctx context.Context, r *Runtime, modCtx *ModuleContext, moduleName string) (inst *ModuleInstance, cleanup func(), hostErr *abi.HostError) {
	target, ok := modCtx.ComputeTargets()[moduleName]
	if !ok || target.Pool == nil {
		return nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "module " + moduleName + " is not available"}
	}

	inst, err := target.Pool.Borrow(ctx)
	if err != nil {
		return nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	depCtx := NewModuleContext(
		modCtx.RequestID, moduleName, modCtx.UserID, modCtx.ContactID, modCtx.Roles,
		modCtx.TenantID, modCtx.TenantSlug, modCtx.TraceID, target.Capabilities, modCtx.txLimiter,
		ModuleSnapshot{
			ModelDecls:       target.ModelDecls,
			FieldSecRegistry: modCtx.FieldSecRegistry(),
			EventRegistry:    modCtx.EventRegistry(),
			ComputedIndex:    modCtx.ComputedIndex(),
			ComputeTargets:   modCtx.ComputeTargets(),
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
// recomputed value.
func invokeCompute(ctx context.Context, r *Runtime, modCtx *ModuleContext, dep computed.Dependent, record map[string]any) (any, *abi.HostError) {
	inst, cleanup, hostErr := borrowModuleInstance(ctx, r, modCtx, dep.ModuleName)
	if hostErr != nil {
		return nil, hostErr
	}
	defer cleanup()

	payload, err := msgpack.Marshal(computeRequest{
		FnName:   dep.ComputeFn,
		Record:   record,
		TenantID: modCtx.TenantID,
		UserID:   modCtx.UserID,
		TraceID:  modCtx.TraceID,
	})
	if err != nil {
		return nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	respBytes, err := inst.InvokeHandleComputed(ctx, payload)
	if err != nil {
		return nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "compute " + dep.Field + ": " + err.Error()}
	}

	var resp computeResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		return nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if resp.Error != nil {
		return nil, &abi.HostError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	return resp.Value, nil
}
