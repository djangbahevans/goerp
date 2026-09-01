package orm

import (
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// ExecResult is WriteMany/WriteWhere's return shape — how many rows
// changed and which ones, without the cost of returning every full
// record body for a call that could touch many rows.
type ExecResult struct {
	Count int      `msgpack:"count"`
	IDs   []string `msgpack:"ids"`
}

type ormWriteInput struct {
	Model        string         `msgpack:"model"`
	ID           string         `msgpack:"id"`
	Record       map[string]any `msgpack:"record"`
	ExpectedEtag string         `msgpack:"expected_etag,omitempty"`
	TxID         string         `msgpack:"tx_id"`
}

type ormWriteOutput struct {
	Record map[string]any `msgpack:"record"`
}

// Write updates one record by ID via host.orm.write. expectedEtag, if
// set, enforces optimistic locking — a mismatch fails with
// orm.etag_mismatch (check via IsEtagMismatch).
func Write(model, id string, vals map[string]any, expectedEtag string) error {
	return write("", model, id, vals, expectedEtag)
}

// WriteTx is Write, scoped to tx's own open transaction.
func WriteTx(tx *db.Tx, model, id string, vals map[string]any, expectedEtag string) error {
	return write(tx.TxID(), model, id, vals, expectedEtag)
}

func write(txID, model, id string, vals map[string]any, expectedEtag string) error {
	var out ormWriteOutput
	return hostcall.Do(hostORMWrite, ormWriteInput{Model: model, ID: id, Record: vals, ExpectedEtag: expectedEtag, TxID: txID}, &out)
}

type ormWriteManyInput struct {
	Model  string         `msgpack:"model"`
	IDs    []string       `msgpack:"ids"`
	Record map[string]any `msgpack:"record"`
	TxID   string         `msgpack:"tx_id"`
}

// WriteMany applies the same field changes to every ID via
// host.orm.write_many — no etag check, since a bulk write has no single
// etag to check against.
func WriteMany(model string, ids []string, vals map[string]any) (ExecResult, error) {
	return writeMany("", model, ids, vals)
}

// WriteManyTx is WriteMany, scoped to tx's own open transaction.
func WriteManyTx(tx *db.Tx, model string, ids []string, vals map[string]any) (ExecResult, error) {
	return writeMany(tx.TxID(), model, ids, vals)
}

func writeMany(txID, model string, ids []string, vals map[string]any) (ExecResult, error) {
	var out ExecResult
	err := hostcall.Do(hostORMWriteMany, ormWriteManyInput{Model: model, IDs: ids, Record: vals, TxID: txID}, &out)
	return out, err
}

type ormWriteWhereInput struct {
	Model  string         `msgpack:"model"`
	Domain string         `msgpack:"domain"`
	Record map[string]any `msgpack:"record"`
	TxID   string         `msgpack:"tx_id"`
}

// WriteWhere applies the same field changes to every record matching
// domain via host.orm.write_where — WriteMany with the ID list resolved
// server-side from domain instead of supplied by the caller.
func WriteWhere(model, domain string, vals map[string]any) (ExecResult, error) {
	return writeWhere("", model, domain, vals)
}

// WriteWhereTx is WriteWhere, scoped to tx's own open transaction.
func WriteWhereTx(tx *db.Tx, model, domain string, vals map[string]any) (ExecResult, error) {
	return writeWhere(tx.TxID(), model, domain, vals)
}

func writeWhere(txID, model, domain string, vals map[string]any) (ExecResult, error) {
	var out ExecResult
	err := hostcall.Do(hostORMWriteWhere, ormWriteWhereInput{Model: model, Domain: domain, Record: vals, TxID: txID}, &out)
	return out, err
}
