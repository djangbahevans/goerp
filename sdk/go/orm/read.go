package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

type ormReadInput = abi.ORMReadInput

type ormReadOutput = abi.ORMReadOutput

// Get fetches one record by ID via host.orm.read, mapping it into a T
// via its own Scan method. Fields defaults to every field the caller's
// field-security context permits when none are given. Returns
// ErrNotFound if id doesn't exist.
func Get[T Model, PT ptrScanner[T]](id string, fields ...AnyField[T]) (T, error) {
	return get[T, PT]("", id, fields)
}

// GetTx is Get, scoped to tx's own open transaction.
func GetTx[T Model, PT ptrScanner[T]](tx *db.Tx, id string, fields ...AnyField[T]) (T, error) {
	return get[T, PT](tx.TxID(), id, fields)
}

func get[T Model, PT ptrScanner[T]](txID, id string, fields []AnyField[T]) (T, error) {
	var zero T
	records, err := read[T, PT](txID, []string{id}, fields)
	if err != nil {
		return zero, err
	}
	if len(records) == 0 {
		return zero, ErrNotFound
	}
	return records[0], nil
}

// GetMany fetches records by ID via host.orm.read, mapping each into a
// T. An ID matching no record is simply absent from the result —
// len(result) can be less than len(ids).
func GetMany[T Model, PT ptrScanner[T]](ids []string, fields ...AnyField[T]) ([]T, error) {
	return read[T, PT]("", ids, fields)
}

// GetManyTx is GetMany, scoped to tx's own open transaction.
func GetManyTx[T Model, PT ptrScanner[T]](tx *db.Tx, ids []string, fields ...AnyField[T]) ([]T, error) {
	return read[T, PT](tx.TxID(), ids, fields)
}

func read[T Model, PT ptrScanner[T]](txID string, ids []string, fields []AnyField[T]) ([]T, error) {
	var out ormReadOutput
	in := ormReadInput{Model: resourceName[T](), IDs: ids, Fields: fieldNames(fields), TxID: txID}
	if err := hostcall.Do(hostORMRead, in, &out); err != nil {
		return nil, err
	}
	return decodeRecords[T, PT](out.Records)
}

// fieldNames converts fields to the bare column-name list host.orm
// takes — nil (not just empty) when fields itself is empty, so the
// request's Fields member stays omitted rather than an explicit empty
// list (host.orm.read's own "empty = every field" default, host-abi-
// reference.md §5a, applies to an omitted Fields the same way it does to
// a literal absence of the member).
func fieldNames[T Model](fields []AnyField[T]) []string {
	if len(fields) == 0 {
		return nil
	}
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name()
	}
	return names
}
