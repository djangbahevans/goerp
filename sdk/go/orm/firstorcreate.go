package orm

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

type ormFirstOrCreateInput struct {
	Model      string         `msgpack:"model"`
	UniqueVals map[string]any `msgpack:"unique_vals"`
	CreateVals map[string]any `msgpack:"create_vals"`
}

type ormFirstOrCreateOutput struct {
	Record  map[string]any `msgpack:"record"`
	Created bool           `msgpack:"created"`
}

// FirstOrCreate finds the record matching uniqueVals, or inserts
// uniqueVals merged with createVals if none matches, via
// host.orm.first_or_create, mapping the result into a T. uniqueVals must
// match a declared unique index (PK or Index(...).Unique()) on model, the
// same rule OnConflictIgnore/OnConflictUpdate's target fields follow —
// see go-sdk-reference.md §6a.
func FirstOrCreate[T any](model string, uniqueVals, createVals map[string]any) (record T, created bool, err error) {
	var zero T
	var out ormFirstOrCreateOutput
	in := ormFirstOrCreateInput{Model: model, UniqueVals: uniqueVals, CreateVals: createVals}
	if err := hostcall.Do(hostORMFirstOrCreate, in, &out); err != nil {
		return zero, false, err
	}
	record, err = decodeRecord[T](out.Record)
	return record, out.Created, err
}
