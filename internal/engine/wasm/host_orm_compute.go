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

// invokeCompute borrows a fresh instance from dep's owning module's pool
// and invokes its registered compute function against record, returning
// the recomputed value. Always borrows a new instance — never the
// currently-executing one, which can't be reentered mid-call — uniform
// whether dep.ModuleName is the same module that triggered the call or a
// different one reached through a Many2One-hop dependency
// (go-sdk-reference.md §22 "Computed field recomputation").
func invokeCompute(ctx context.Context, r *Runtime, modCtx *ModuleContext, dep computed.Dependent, record map[string]any) (any, *abi.HostError) {
	target, ok := modCtx.ComputeTargets()[dep.ModuleName]
	if !ok || target.Pool == nil {
		return nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "module " + dep.ModuleName + " is not available to compute " + dep.Field}
	}

	inst, err := target.Pool.Borrow(ctx)
	if err != nil {
		return nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	defer target.Pool.Return(inst)

	// depCtx carries the triggering request's identity (tenant/user/
	// trace) into the owning module's own model/capability scope, so a
	// compute function's own host.orm calls (e.g. looking up related
	// data) resolve against its own declared models exactly as if this
	// were a normal request to that module.
	depCtx := NewModuleContext(
		modCtx.RequestID, dep.ModuleName, modCtx.UserID, modCtx.ContactID, modCtx.Roles,
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
	defer func() {
		r.UnregisterInstance(inst)
		depCtx.RollbackAll()
		inst.SetModuleContext(nil)
	}()

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
