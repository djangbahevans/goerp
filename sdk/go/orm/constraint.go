package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/wasmmem"
	"github.com/vmihailenco/msgpack/v5"
)

// ConstraintPhase names when a registered constraint hook runs.
type ConstraintPhase string

const (
	OnCreate ConstraintPhase = "create" // before insert
	OnWrite  ConstraintPhase = "write"  // before update
	OnDelete ConstraintPhase = "delete" // before soft-delete
)

// ConstraintContext carries the request-scoped identity a constraint hook
// needs to make its own host calls (e.g. host.orm.read for related data)
// — the same information a request handler already has.
type ConstraintContext struct {
	TenantID string
	UserID   string
	TraceID  string
}

// ConstraintResult is opaque — construct one via Reject or Allow.
type ConstraintResult struct {
	allowed bool
	field   string
	message string
}

// Reject aborts the triggering Create, Write or Unlink. The caller receives
// orm.validation_failed with details.field set, the same error a declared
// field constraint such as .Required() produces.
func Reject(field, message string) *ConstraintResult {
	return &ConstraintResult{allowed: false, field: field, message: message}
}

// Allow lets the triggering write proceed.
func Allow() *ConstraintResult {
	return &ConstraintResult{allowed: true}
}

// ConstraintFunc validates record against a business rule beyond what a
// field-shaped declaration (.Required(), uniqueness, .Domain()) can
// express. record is the record as it will exist after the triggering
// write (post computed-field recompute) for OnCreate/OnWrite, or the
// existing record about to be deleted for OnDelete.
type ConstraintFunc func(ctx ConstraintContext, record map[string]any) *ConstraintResult

type constraintKey struct {
	model string
	phase ConstraintPhase
}

var constraintRegistry = map[constraintKey]ConstraintFunc{}

// RegisterConstraint associates (modelName, phase) with the function that
// validates it. Call from init().
func RegisterConstraint(modelName string, phase ConstraintPhase, fn ConstraintFunc) {
	constraintRegistry[constraintKey{model: modelName, phase: phase}] = fn
}

// DispatchConstraint decodes an abi.ConstraintRequest from module memory at
// (ptr, length), routes it to the ConstraintFunc registered for
// (req.Model, req.Phase), and writes back a msgpack-encoded
// abi.ConstraintResponse. A (model, phase) with no registered hook is
// allowed. A module exports this as
//
//	//go:wasmexport handle_orm_constraint
//	func handleOrmConstraint(ptr, length uint32) uint64 { return orm.DispatchConstraint(ptr, length) }
func DispatchConstraint(ptr, length uint32) uint64 {
	buf := wasmmem.ReadMem(ptr, length)

	var req abi.ConstraintRequest
	if err := msgpack.Unmarshal(buf, &req); err != nil {
		return writeConstraintResponse(&abi.ConstraintResponse{Error: &abi.ConstraintError{Code: "orm.invalid_request", Message: err.Error()}})
	}

	fn, ok := constraintRegistry[constraintKey{model: req.Model, phase: ConstraintPhase(req.Phase)}]
	if !ok {
		return writeConstraintResponse(&abi.ConstraintResponse{Allowed: true})
	}

	ctx := ConstraintContext{TenantID: req.TenantID, UserID: req.UserID, TraceID: req.TraceID}
	result := fn(ctx, req.Record)
	return writeConstraintResponse(&abi.ConstraintResponse{Allowed: result.allowed, Field: result.field, Message: result.message})
}

func writeConstraintResponse(resp *abi.ConstraintResponse) uint64 {
	data, err := msgpack.Marshal(resp)
	if err != nil {
		data, _ = msgpack.Marshal(&abi.ConstraintResponse{
			Error: &abi.ConstraintError{Code: "orm.marshal_failed", Message: err.Error()},
		})
	}
	ptr := wasmmem.Allocate(uint32(len(data)))
	wasmmem.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}
