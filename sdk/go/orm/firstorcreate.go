package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

type ormFirstOrCreateInput = abi.ORMFirstOrCreateInput

type ormFirstOrCreateOutput = abi.ORMFirstOrCreateOutput

// FirstOrCreate finds the record matching uniqueVals, or inserts
// uniqueVals merged with createVals if none matches, via
// host.orm.first_or_create, mapping the result into a T. uniqueVals must
// match a declared unique index (PK or Index(...).Unique()) on model, the
// same rule OnConflictIgnore/OnConflictUpdate's target fields follow —
// see go-sdk-reference.md §6a.
func FirstOrCreate[T any](model string, uniqueVals, createVals map[string]any) (record T, created bool, err error) {
	return firstOrCreate[T]("", model, uniqueVals, createVals)
}

// FirstOrCreateTx is FirstOrCreate, scoped to tx's own open transaction.
func FirstOrCreateTx[T any](tx *db.Tx, model string, uniqueVals, createVals map[string]any) (record T, created bool, err error) {
	return firstOrCreate[T](tx.TxID(), model, uniqueVals, createVals)
}

func firstOrCreate[T any](txID, model string, uniqueVals, createVals map[string]any) (record T, created bool, err error) {
	var zero T
	var out ormFirstOrCreateOutput
	in := ormFirstOrCreateInput{Model: model, UniqueVals: uniqueVals, CreateVals: createVals, TxID: txID}
	if err := hostcall.Do(hostORMFirstOrCreate, in, &out); err != nil {
		return zero, false, err
	}
	record, err = decodeRecord[T](out.Record)
	return record, out.Created, err
}
