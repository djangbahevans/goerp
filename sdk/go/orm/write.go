package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// ExecResult is WriteMany/WriteWhere's return shape — how many rows
// changed and which ones, without the cost of returning every full
// record body for a call that could touch many rows.
type ExecResult = abi.ORMExecResult

type ormWriteInput = abi.ORMWriteInput

type ormWriteOutput = abi.ORMWriteOutput

// Write updates one record by ID via host.orm.write. A nil expectedEtag
// writes unconditionally; a non-nil expectedEtag enforces optimistic
// locking against the stored value — including a pointer to "" for a
// record that has never been written since it was created (the etag
// column's own default) — and a mismatch fails with orm.etag_mismatch
// (check via IsEtagMismatch).
func Write[T Model](id string, vals *Values[T], expectedEtag *string) error {
	return write("", id, vals, expectedEtag)
}

// WriteTx is Write, scoped to tx's own open transaction.
func WriteTx[T Model](tx *db.Tx, id string, vals *Values[T], expectedEtag *string) error {
	return write(tx.TxID(), id, vals, expectedEtag)
}

func write[T Model](txID, id string, vals *Values[T], expectedEtag *string) error {
	var out ormWriteOutput
	in := ormWriteInput{Model: resourceName[T](), ID: id, Record: vals.raw(), ExpectedEtag: expectedEtag, TxID: txID}
	return hostcall.Do(hostORMWrite, in, &out)
}

type ormWriteManyInput = abi.ORMWriteManyInput

// WriteMany applies the same field changes to every ID via
// host.orm.write_many — no etag check, since a bulk write has no single
// etag to check against.
func WriteMany[T Model](ids []string, vals *Values[T]) (ExecResult, error) {
	return writeMany("", ids, vals)
}

// WriteManyTx is WriteMany, scoped to tx's own open transaction.
func WriteManyTx[T Model](tx *db.Tx, ids []string, vals *Values[T]) (ExecResult, error) {
	return writeMany(tx.TxID(), ids, vals)
}

func writeMany[T Model](txID string, ids []string, vals *Values[T]) (ExecResult, error) {
	var out ExecResult
	in := ormWriteManyInput{Model: resourceName[T](), IDs: ids, Record: vals.raw(), TxID: txID}
	err := hostcall.Do(hostORMWriteMany, in, &out)
	return out, err
}

type ormWriteWhereInput = abi.ORMWriteWhereInput

// WriteWhere applies the same field changes to every record matching
// cond via host.orm.write_where — WriteMany with the ID list resolved
// server-side from cond instead of supplied by the caller. cond must be
// a BoundCondition[T] — the explicit Bind()/Unbounded() acknowledgment
// that a Condition[T] alone doesn't give, so a forgotten filter can't
// silently touch every row:
//
//	var c orm.Condition[Widget]
//	orm.WriteWhere(c, vals)        // compile error: Condition[Widget] is
//	                                // not BoundCondition[Widget]
//	orm.WriteWhere(c.Bind(), vals) // fine — c explicitly acknowledged
//	orm.WriteWhere(orm.Unbounded[Widget](), vals) // fine — every record, explicitly
func WriteWhere[T Model](cond BoundCondition[T], vals *Values[T]) (ExecResult, error) {
	return writeWhere("", cond, vals)
}

// WriteWhereTx is WriteWhere, scoped to tx's own open transaction.
func WriteWhereTx[T Model](tx *db.Tx, cond BoundCondition[T], vals *Values[T]) (ExecResult, error) {
	return writeWhere(tx.TxID(), cond, vals)
}

func writeWhere[T Model](txID string, cond BoundCondition[T], vals *Values[T]) (ExecResult, error) {
	var out ExecResult
	in := ormWriteWhereInput{Model: resourceName[T](), Domain: cond.expr, Record: vals.raw(), TxID: txID}
	err := hostcall.Do(hostORMWriteWhere, in, &out)
	return out, err
}
