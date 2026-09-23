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
type MutateOption[T Model] func(*mutateSpec)

// Increment adds n to f. n's Go type is bound to f's own declared type,
// so incrementing a field by a mismatched numeric type is a compile
// error instead of a round trip that fails orm.validation_failed.
func Increment[T Model, N Number](f Field[T, N], n N) MutateOption[T] {
	return func(s *mutateSpec) { s.ops = append(s.ops, abi.ORMMutateOp{Field: f.Name(), Delta: n}) }
}

// Decrement subtracts n from f.
func Decrement[T Model, N Number](f Field[T, N], n N) MutateOption[T] {
	return func(s *mutateSpec) { s.ops = append(s.ops, abi.ORMMutateOp{Field: f.Name(), Delta: -n}) }
}

// Where guards the mutation with cond, evaluated against the record's
// current state; a false guard fails with orm.precondition_failed
// (check via IsPreconditionFailed) and changes nothing.
func Where[T Model](cond Condition[T]) MutateOption[T] {
	return func(s *mutateSpec) { s.guard = cond.expr }
}

// Mutate applies the Increment/Decrement ops to one record via
// host.orm.mutate as a single guarded UPDATE, so concurrent callers cannot
// lose updates or pass a Where guard on stale state. It returns the
// updated record mapped into a T, without fields the caller cannot read.
func Mutate[T Model, PT ptrScanner[T]](id string, opts ...MutateOption[T]) (T, error) {
	return mutate[T, PT]("", id, opts...)
}

// MutateTx is Mutate, scoped to tx's own open transaction.
func MutateTx[T Model, PT ptrScanner[T]](tx *db.Tx, id string, opts ...MutateOption[T]) (T, error) {
	return mutate[T, PT](tx.TxID(), id, opts...)
}

func mutate[T Model, PT ptrScanner[T]](txID, id string, opts ...MutateOption[T]) (T, error) {
	var zero T
	var spec mutateSpec
	for _, opt := range opts {
		opt(&spec)
	}
	var out ormMutateOutput
	in := ormMutateInput{Model: resourceName[T](), ID: id, Ops: spec.ops, Guard: spec.guard, TxID: txID}
	if err := hostcall.Do(hostORMMutate, in, &out); err != nil {
		return zero, err
	}
	return decodeRecord[T, PT](out.Record)
}
