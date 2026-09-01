package orm

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

type ormReadInput struct {
	Model  string   `msgpack:"model"`
	IDs    []string `msgpack:"ids"`
	Fields []string `msgpack:"fields,omitempty"`
}

type ormReadOutput struct {
	Records []map[string]any `msgpack:"records"`
}

// Read fetches records by ID via host.orm.read, mapping each into a T
// via its own db-tag-mapped fields. An ID matching no record is simply
// absent from the result — len(result) can be less than len(ids).
func Read[T any](model string, ids []string, fields []string) ([]T, error) {
	var out ormReadOutput
	if err := hostcall.Do(hostORMRead, ormReadInput{Model: model, IDs: ids, Fields: fields}, &out); err != nil {
		return nil, err
	}
	return decodeRecords[T](out.Records)
}

// ReadOne fetches a single record by ID, returning ErrNotFound if it
// doesn't exist.
func ReadOne[T any](model, id string, fields []string) (T, error) {
	var zero T
	records, err := Read[T](model, []string{id}, fields)
	if err != nil {
		return zero, err
	}
	if len(records) == 0 {
		return zero, ErrNotFound
	}
	return records[0], nil
}
