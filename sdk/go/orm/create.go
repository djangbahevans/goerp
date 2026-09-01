package orm

import (
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// ormOnConflict is Create/CreateBatch's own idempotent-insert wire shape
// — Policy is "ignore" or "update".
type ormOnConflict struct {
	Fields []string `msgpack:"fields"`
	Policy string   `msgpack:"policy"`
}

type ormCreateInput struct {
	Model      string         `msgpack:"model"`
	Record     map[string]any `msgpack:"record"`
	OnConflict *ormOnConflict `msgpack:"on_conflict,omitempty"`
	TxID       string         `msgpack:"tx_id"`
}

type ormCreateOutput struct {
	Record map[string]any `msgpack:"record"`
}

type ormCreateBatchInput struct {
	Model      string           `msgpack:"model"`
	Records    []map[string]any `msgpack:"records"`
	OnConflict *ormOnConflict   `msgpack:"on_conflict,omitempty"`
	TxID       string           `msgpack:"tx_id"`
}

type ormCreateBatchOutput struct {
	Records []map[string]any `msgpack:"records"`
}

type createOpts struct {
	OnConflict *ormOnConflict
}

// CreateOption configures Create/CreateBatch — OnConflictIgnore,
// OnConflictUpdate.
type CreateOption func(*createOpts)

// OnConflictIgnore returns the existing row instead of erroring when one
// already matches uniqueFields — fields must match a declared unique
// index (PK or Index(...).Unique()); the engine returns
// orm.conflict_target_invalid otherwise.
func OnConflictIgnore(uniqueFields ...string) CreateOption {
	return func(o *createOpts) { o.OnConflict = &ormOnConflict{Fields: uniqueFields, Policy: "ignore"} }
}

// OnConflictUpdate updates the existing row with the call's own vals
// instead of erroring when one already matches uniqueFields — same
// match rule as OnConflictIgnore.
func OnConflictUpdate(uniqueFields ...string) CreateOption {
	return func(o *createOpts) { o.OnConflict = &ormOnConflict{Fields: uniqueFields, Policy: "update"} }
}

// Create inserts one record via host.orm.create, mapping the result into
// a T via its own db-tag-mapped fields.
func Create[T any](model string, vals map[string]any, opts ...CreateOption) (T, error) {
	return create[T]("", model, vals, opts...)
}

// CreateTx is Create, scoped to tx's own open transaction.
func CreateTx[T any](tx *db.Tx, model string, vals map[string]any, opts ...CreateOption) (T, error) {
	return create[T](tx.TxID(), model, vals, opts...)
}

func create[T any](txID, model string, vals map[string]any, opts ...CreateOption) (T, error) {
	var zero T
	var o createOpts
	for _, opt := range opts {
		opt(&o)
	}
	var out ormCreateOutput
	in := ormCreateInput{Model: model, Record: vals, OnConflict: o.OnConflict, TxID: txID}
	if err := hostcall.Do(hostORMCreate, in, &out); err != nil {
		return zero, err
	}
	return decodeRecord[T](out.Record)
}

// CreateBatch inserts multiple records in one call via
// host.orm.create_batch, mapping each result into a T — opts included,
// since host.orm.create_batch already fully supports on_conflict
// (host-abi-reference.md §5a).
func CreateBatch[T any](model string, valsList []map[string]any, opts ...CreateOption) ([]T, error) {
	return createBatch[T]("", model, valsList, opts...)
}

// CreateBatchTx is CreateBatch, scoped to tx's own open transaction.
func CreateBatchTx[T any](tx *db.Tx, model string, valsList []map[string]any, opts ...CreateOption) ([]T, error) {
	return createBatch[T](tx.TxID(), model, valsList, opts...)
}

func createBatch[T any](txID, model string, valsList []map[string]any, opts ...CreateOption) ([]T, error) {
	var o createOpts
	for _, opt := range opts {
		opt(&o)
	}
	var out ormCreateBatchOutput
	in := ormCreateBatchInput{Model: model, Records: valsList, OnConflict: o.OnConflict, TxID: txID}
	if err := hostcall.Do(hostORMCreateBatch, in, &out); err != nil {
		return nil, err
	}
	return decodeRecords[T](out.Records)
}
