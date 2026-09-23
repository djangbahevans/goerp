package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

type ormReadInput = abi.ORMReadInput

type ormReadOutput = abi.ORMReadOutput

// Read fetches records by ID via host.orm.read, mapping each into a T
// via its own Scan method. An ID matching no record is simply absent
// from the result — len(result) can be less than len(ids).
func Read[T any, PT ptrScanner[T]](model string, ids []string, fields []string) ([]T, error) {
	return read[T, PT]("", model, ids, fields)
}

// ReadTx is Read, scoped to tx's own open transaction.
func ReadTx[T any, PT ptrScanner[T]](tx *db.Tx, model string, ids []string, fields []string) ([]T, error) {
	return read[T, PT](tx.TxID(), model, ids, fields)
}

func read[T any, PT ptrScanner[T]](txID, model string, ids []string, fields []string) ([]T, error) {
	var out ormReadOutput
	if err := hostcall.Do(hostORMRead, ormReadInput{Model: model, IDs: ids, Fields: fields, TxID: txID}, &out); err != nil {
		return nil, err
	}
	return decodeRecords[T, PT](out.Records)
}

// ReadOne fetches a single record by ID, returning ErrNotFound if it
// doesn't exist.
func ReadOne[T any, PT ptrScanner[T]](model, id string, fields []string) (T, error) {
	return readOne[T, PT]("", model, id, fields)
}

// ReadOneTx is ReadOne, scoped to tx's own open transaction.
func ReadOneTx[T any, PT ptrScanner[T]](tx *db.Tx, model, id string, fields []string) (T, error) {
	return readOne[T, PT](tx.TxID(), model, id, fields)
}

func readOne[T any, PT ptrScanner[T]](txID, model, id string, fields []string) (T, error) {
	var zero T
	records, err := read[T, PT](txID, model, []string{id}, fields)
	if err != nil {
		return zero, err
	}
	if len(records) == 0 {
		return zero, ErrNotFound
	}
	return records[0], nil
}
