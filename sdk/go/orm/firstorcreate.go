package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// FirstOrCreate finds the record matching unique, or inserts unique
// merged with create if none matches, via host.orm.first_or_create,
// mapping the result into a T. unique must match a declared unique
// index (PK or Index(...).Unique()) on T, the same rule
// OnConflictIgnore/OnConflictUpdate's target fields follow — see
// go-sdk-reference.md §6a.
func FirstOrCreate[T Model, PT ptrScanner[T]](unique, create *Values[T]) (record T, created bool, err error) {
	return firstOrCreate[T, PT]("", unique, create)
}

// FirstOrCreateTx is FirstOrCreate, scoped to tx's own open transaction.
func FirstOrCreateTx[T Model, PT ptrScanner[T]](tx *db.Tx, unique, create *Values[T]) (record T, created bool, err error) {
	return firstOrCreate[T, PT](tx.TxID(), unique, create)
}

func firstOrCreate[T Model, PT ptrScanner[T]](txID string, unique, create *Values[T]) (record T, created bool, err error) {
	var zero T
	var out abi.ORMFirstOrCreateOutput
	in := abi.ORMFirstOrCreateInput{Model: resourceName[T](), UniqueVals: unique.raw(), CreateVals: create.raw(), TxID: txID}
	if err := hostcall.Do(hostORMFirstOrCreate, in, &out); err != nil {
		return zero, false, err
	}
	record, err = decodeRecord[T, PT](out.Record)
	return record, out.Created, err
}
