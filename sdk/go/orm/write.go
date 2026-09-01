package orm

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

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
}

type ormWriteOutput struct {
	Record map[string]any `msgpack:"record"`
}

// Write updates one record by ID via host.orm.write. expectedEtag, if
// set, enforces optimistic locking — a mismatch fails with
// orm.etag_mismatch (check via IsEtagMismatch).
func Write(model, id string, vals map[string]any, expectedEtag string) error {
	var out ormWriteOutput
	return hostcall.Do(hostORMWrite, ormWriteInput{Model: model, ID: id, Record: vals, ExpectedEtag: expectedEtag}, &out)
}

type ormWriteManyInput struct {
	Model  string         `msgpack:"model"`
	IDs    []string       `msgpack:"ids"`
	Record map[string]any `msgpack:"record"`
}

// WriteMany applies the same field changes to every ID via
// host.orm.write_many — no etag check, since a bulk write has no single
// etag to check against.
func WriteMany(model string, ids []string, vals map[string]any) (ExecResult, error) {
	var out ExecResult
	err := hostcall.Do(hostORMWriteMany, ormWriteManyInput{Model: model, IDs: ids, Record: vals}, &out)
	return out, err
}

type ormWriteWhereInput struct {
	Model  string         `msgpack:"model"`
	Domain string         `msgpack:"domain"`
	Record map[string]any `msgpack:"record"`
}

// WriteWhere applies the same field changes to every record matching
// domain via host.orm.write_where — WriteMany with the ID list resolved
// server-side from domain instead of supplied by the caller.
func WriteWhere(model, domain string, vals map[string]any) (ExecResult, error) {
	var out ExecResult
	err := hostcall.Do(hostORMWriteWhere, ormWriteWhereInput{Model: model, Domain: domain, Record: vals}, &out)
	return out, err
}
