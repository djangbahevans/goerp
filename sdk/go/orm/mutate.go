package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

type ormMutateInput = abi.ORMMutateInput

type ormMutateOutput = abi.ORMMutateOutput

// Number is the delta type of Increment and Decrement.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~float64
}

type mutateSpec struct {
	ops   []abi.ORMMutateOp
	guard string
}

// MutateOption configures Mutate — Increment, Decrement, Where.
type MutateOption func(*mutateSpec)

// Increment adds n to field.
func Increment[N Number](field string, n N) MutateOption {
	return func(s *mutateSpec) { s.ops = append(s.ops, abi.ORMMutateOp{Field: field, Delta: n}) }
}

// Decrement subtracts n from field.
func Decrement[N Number](field string, n N) MutateOption {
	return func(s *mutateSpec) { s.ops = append(s.ops, abi.ORMMutateOp{Field: field, Delta: -n}) }
}

// Where guards the mutation with a domain expression evaluated against
// the record's current state; a false guard fails with
// orm.precondition_failed (check via IsPreconditionFailed) and changes
// nothing.
func Where(domain string) MutateOption {
	return func(s *mutateSpec) { s.guard = domain }
}

// Mutate applies the Increment/Decrement ops to one record via
// host.orm.mutate as a single guarded UPDATE, so concurrent callers cannot
// lose updates or pass a Where guard on stale state. It returns the
// updated record mapped into a T, without fields the caller cannot read.
func Mutate[T any](model, id string, opts ...MutateOption) (T, error) {
	return mutate[T]("", model, id, opts...)
}

// MutateTx is Mutate, scoped to tx's own open transaction.
func MutateTx[T any](tx *db.Tx, model, id string, opts ...MutateOption) (T, error) {
	return mutate[T](tx.TxID(), model, id, opts...)
}

func mutate[T any](txID, model, id string, opts ...MutateOption) (T, error) {
	var zero T
	var spec mutateSpec
	for _, opt := range opts {
		opt(&spec)
	}
	var out ormMutateOutput
	in := ormMutateInput{Model: model, ID: id, Ops: spec.ops, Guard: spec.guard, TxID: txID}
	if err := hostcall.Do(hostORMMutate, in, &out); err != nil {
		return zero, err
	}
	return decodeRecord[T](out.Record)
}
