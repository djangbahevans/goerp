package orm

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

type ormFirstOrCreateInput struct {
	Model  string         `msgpack:"model"`
	Domain string         `msgpack:"domain"`
	Record map[string]any `msgpack:"record"`
}

type ormFirstOrCreateOutput struct {
	Record  map[string]any `msgpack:"record"`
	Created bool           `msgpack:"created"`
}

// FirstOrCreate finds the first record matching domain, or inserts vals
// if none matches, via host.orm.first_or_create, mapping the result into
// a T. Matches by an arbitrary domain expression, unlike
// go-sdk-reference.md §6a's own documented uniqueVals-based signature —
// host.orm.first_or_create doesn't validate against a declared unique
// index yet (goerp#542).
func FirstOrCreate[T any](model, domain string, vals map[string]any) (record T, created bool, err error) {
	var zero T
	var out ormFirstOrCreateOutput
	in := ormFirstOrCreateInput{Model: model, Domain: domain, Record: vals}
	if err := hostcall.Do(hostORMFirstOrCreate, in, &out); err != nil {
		return zero, false, err
	}
	record, err = decodeRecord[T](out.Record)
	return record, out.Created, err
}
