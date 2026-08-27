// This file is sdk/go/orm's outbound module-side caller half —
// Search/SearchRead/Read/Create/CreateBatch/FirstOrCreate/Write/
// WriteMany/WriteWhere/Unlink, calling the matching host.orm.* function
// (host-abi-reference.md §5a) via sdk/go/internal/hostcall. Every
// input/output type here mirrors the exact msgpack shape
// internal/engine/wasm/host_orm.go/host_orm_write.go's own (unexported
// to that package) wire types use — duplicated deliberately rather than
// imported, since this package compiles into a module's own wasip1
// binary and can't depend on engine internals.
package orm

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

// OnConflict configures Create/CreateBatch's idempotent-insert behaviour
// — Policy is "ignore" or "update".
type OnConflict struct {
	Fields []string `msgpack:"fields"`
	Policy string   `msgpack:"policy"`
}

// ExecResult is WriteMany/WriteWhere/Unlink's return shape — how many
// rows changed and which ones, without the cost of returning every full
// record body for a call that could touch many rows.
type ExecResult struct {
	Count int      `msgpack:"count"`
	IDs   []string `msgpack:"ids"`
}

type SearchInput struct {
	Model  string `msgpack:"model"`
	Domain string `msgpack:"domain"`
	Order  string `msgpack:"order,omitempty"`
	Limit  int    `msgpack:"limit,omitempty"`
	Offset int    `msgpack:"offset,omitempty"`
}

type SearchOutput struct {
	IDs   []string `msgpack:"ids"`
	Count int64    `msgpack:"count"`
}

// Search finds matching record IDs via host.orm.search. Domain is the
// domain expression language (manifest-spec.md §8), not raw SQL.
func Search(in SearchInput) (SearchOutput, error) {
	var out SearchOutput
	err := hostcall.Do(hostORMSearch, in, &out)
	return out, err
}

type SearchReadInput struct {
	Model  string   `msgpack:"model"`
	Domain string   `msgpack:"domain"`
	Fields []string `msgpack:"fields,omitempty"`
	Order  string   `msgpack:"order,omitempty"`
	Limit  int      `msgpack:"limit,omitempty"`
	Offset int      `msgpack:"offset,omitempty"`
	Cursor string   `msgpack:"cursor,omitempty"`
}

type SearchReadOutput struct {
	Records    []map[string]any `msgpack:"records"`
	NextCursor string           `msgpack:"next_cursor,omitempty"`
}

// SearchRead finds and reads matching records in one call via
// host.orm.search_read.
func SearchRead(in SearchReadInput) (SearchReadOutput, error) {
	var out SearchReadOutput
	err := hostcall.Do(hostORMSearchRead, in, &out)
	return out, err
}

type ReadInput struct {
	Model  string   `msgpack:"model"`
	IDs    []string `msgpack:"ids"`
	Fields []string `msgpack:"fields,omitempty"`
}

type ReadOutput struct {
	Records []map[string]any `msgpack:"records"`
}

// Read fetches records by ID via host.orm.read.
func Read(in ReadInput) (ReadOutput, error) {
	var out ReadOutput
	err := hostcall.Do(hostORMRead, in, &out)
	return out, err
}

type CreateInput struct {
	Model      string         `msgpack:"model"`
	Record     map[string]any `msgpack:"record"`
	OnConflict *OnConflict    `msgpack:"on_conflict,omitempty"`
}

type CreateOutput struct {
	Record map[string]any `msgpack:"record"`
}

// Create inserts one record via host.orm.create.
func Create(in CreateInput) (CreateOutput, error) {
	var out CreateOutput
	err := hostcall.Do(hostORMCreate, in, &out)
	return out, err
}

type CreateBatchInput struct {
	Model      string           `msgpack:"model"`
	Records    []map[string]any `msgpack:"records"`
	OnConflict *OnConflict      `msgpack:"on_conflict,omitempty"`
}

type CreateBatchOutput struct {
	Records []map[string]any `msgpack:"records"`
}

// CreateBatch inserts multiple records in one call via
// host.orm.create_batch.
func CreateBatch(in CreateBatchInput) (CreateBatchOutput, error) {
	var out CreateBatchOutput
	err := hostcall.Do(hostORMCreateBatch, in, &out)
	return out, err
}

type FirstOrCreateInput struct {
	Model  string         `msgpack:"model"`
	Domain string         `msgpack:"domain"`
	Record map[string]any `msgpack:"record"`
}

type FirstOrCreateOutput struct {
	Record  map[string]any `msgpack:"record"`
	Created bool           `msgpack:"created"`
}

// FirstOrCreate finds the first record matching Domain, or inserts
// Record if none matches, via host.orm.first_or_create.
func FirstOrCreate(in FirstOrCreateInput) (FirstOrCreateOutput, error) {
	var out FirstOrCreateOutput
	err := hostcall.Do(hostORMFirstOrCreate, in, &out)
	return out, err
}

type WriteInput struct {
	Model        string         `msgpack:"model"`
	ID           string         `msgpack:"id"`
	Record       map[string]any `msgpack:"record"`
	ExpectedEtag string         `msgpack:"expected_etag,omitempty"`
}

type WriteOutput struct {
	Record map[string]any `msgpack:"record"`
}

// Write updates one record by ID via host.orm.write. ExpectedEtag, if
// set, enforces optimistic locking — a mismatch fails with
// orm.etag_mismatch.
func Write(in WriteInput) (WriteOutput, error) {
	var out WriteOutput
	err := hostcall.Do(hostORMWrite, in, &out)
	return out, err
}

type WriteManyInput struct {
	Model  string         `msgpack:"model"`
	IDs    []string       `msgpack:"ids"`
	Record map[string]any `msgpack:"record"`
}

// WriteMany applies the same field changes to every ID via
// host.orm.write_many.
func WriteMany(in WriteManyInput) (ExecResult, error) {
	var out ExecResult
	err := hostcall.Do(hostORMWriteMany, in, &out)
	return out, err
}

type WriteWhereInput struct {
	Model  string         `msgpack:"model"`
	Domain string         `msgpack:"domain"`
	Record map[string]any `msgpack:"record"`
}

// WriteWhere applies the same field changes to every record matching
// Domain via host.orm.write_where.
func WriteWhere(in WriteWhereInput) (ExecResult, error) {
	var out ExecResult
	err := hostcall.Do(hostORMWriteWhere, in, &out)
	return out, err
}

type UnlinkInput struct {
	Model string `msgpack:"model"`
	ID    string `msgpack:"id"`
}

type UnlinkOutput struct {
	Deleted bool `msgpack:"deleted"`
}

// Unlink deletes one record by ID via host.orm.unlink.
func Unlink(in UnlinkInput) (UnlinkOutput, error) {
	var out UnlinkOutput
	err := hostcall.Do(hostORMUnlink, in, &out)
	return out, err
}
