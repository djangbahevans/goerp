package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

type ormUnlinkInput = abi.ORMUnlinkInput

// Unlink deletes records by ID via host.orm.unlink. A missing ID aborts
// the whole call, so a returned ExecResult always has Count == len(ids)
// — matching WriteMany/WriteWhere's own all-or-nothing semantics for a
// SQL-backed model. A model.Transient() model has no transaction to roll
// back: a missing ID partway through still aborts the call, but any
// earlier ID in the same list is already deleted for good, not undone.
func Unlink[T Model](ids ...string) (ExecResult, error) {
	return unlink[T]("", ids)
}

// UnlinkTx is Unlink, scoped to tx's own open transaction.
func UnlinkTx[T Model](tx *db.Tx, ids ...string) (ExecResult, error) {
	return unlink[T](tx.TxID(), ids)
}

func unlink[T Model](txID string, ids []string) (ExecResult, error) {
	var out ExecResult
	err := hostcall.Do(hostORMUnlink, ormUnlinkInput{Model: resourceName[T](), IDs: ids, TxID: txID}, &out)
	return out, err
}
