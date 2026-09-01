package orm

import (
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

type ormUnlinkInput struct {
	Model string   `msgpack:"model"`
	IDs   []string `msgpack:"ids"`
	TxID  string   `msgpack:"tx_id"`
}

// Unlink deletes records by ID via host.orm.unlink. A missing ID aborts
// the whole call, so a returned ExecResult always has Count == len(ids)
// — matching WriteMany/WriteWhere's own all-or-nothing semantics for a
// SQL-backed model. A model.Transient() model has no transaction to roll
// back: a missing ID partway through still aborts the call, but any
// earlier ID in the same list is already deleted for good, not undone.
func Unlink(model string, ids []string) (ExecResult, error) {
	return unlink("", model, ids)
}

// UnlinkTx is Unlink, scoped to tx's own open transaction.
func UnlinkTx(tx *db.Tx, model string, ids []string) (ExecResult, error) {
	return unlink(tx.TxID(), model, ids)
}

func unlink(txID, model string, ids []string) (ExecResult, error) {
	var out ExecResult
	err := hostcall.Do(hostORMUnlink, ormUnlinkInput{Model: model, IDs: ids, TxID: txID}, &out)
	return out, err
}
