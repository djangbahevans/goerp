package orm

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

// ComputeContext carries the request-scoped identity a compute function
// needs to make its own host calls (e.g. host.orm.read for related data
// beyond what record already carries) — the same information a request
// handler already has.
type ComputeContext struct {
	TenantID string
	UserID   string
	TraceID  string
}

// ComputeFunc computes a Computed field's value from the current state of
// the record it's declared on. For a same-record dependency, record is
// the record being written; for a Many2One-hop dependency, record is the
// dependent record whose field is recomputing (not the record that
// triggered the write) — go-sdk-reference.md §22 "Computed field
// recomputation".
type ComputeFunc func(ctx ComputeContext, record map[string]any) (any, error)

var computeRegistry = map[string]ComputeFunc{}

// RegisterComputed associates fnName (the string a .Computed(fnName)
// field declaration names) with the function that computes its value.
// Call from init().
func RegisterComputed(fnName string, fn ComputeFunc) {
	computeRegistry[fnName] = fn
}

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

// DispatchComputed decodes a computeRequest from module memory at (ptr,
// length), routes it to the registered ComputeFunc named by req.FnName,
// and writes back a msgpack-encoded computeResponse — the same
// decode/route/encode shape orm.DispatchVirtualOp (virtual.go) already
// uses for handle_virtual_op, dispatching by fn_name instead of a
// (model, op) pair. A module exports this as
//
//	//go:wasmexport handle_orm_compute
//	func handleOrmCompute(ptr, length uint32) uint64 { return orm.DispatchComputed(ptr, length) }
func DispatchComputed(ptr, length uint32) uint64 {
	buf := engine.ReadMem(ptr, length)

	var req computeRequest
	if err := msgpack.Unmarshal(buf, &req); err != nil {
		return writeComputeError("orm.invalid_request", err.Error())
	}

	fn, ok := computeRegistry[req.FnName]
	if !ok {
		return writeComputeError("orm.compute_fn_not_registered", "no compute function registered for "+req.FnName)
	}

	ctx := ComputeContext{TenantID: req.TenantID, UserID: req.UserID, TraceID: req.TraceID}

	value, err := fn(ctx, req.Record)
	if err != nil {
		return writeComputeError("orm.backend_error", err.Error())
	}
	return writeComputeResponse(&computeResponse{Value: value})
}

func writeComputeError(code, message string) uint64 {
	return writeComputeResponse(&computeResponse{Error: &computeError{Code: code, Message: message}})
}

func writeComputeResponse(resp *computeResponse) uint64 {
	data, err := msgpack.Marshal(resp)
	if err != nil {
		data, _ = msgpack.Marshal(&computeResponse{
			Error: &computeError{Code: "orm.marshal_failed", Message: err.Error()},
		})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}
