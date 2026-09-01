package orm

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

// ormSearchInput omits host.orm.search's own offset field — Search has
// no public Offset SearchOption (go-sdk-reference.md §6a steers callers
// toward Cursor instead, the same reasoning SearchRead's own option set
// already follows), so it's never non-zero here.
type ormSearchInput struct {
	Model  string `msgpack:"model"`
	Domain string `msgpack:"domain"`
	Order  string `msgpack:"order,omitempty"`
	Limit  int    `msgpack:"limit,omitempty"`
}

type ormSearchOutput struct {
	IDs   []string `msgpack:"ids"`
	Count int64    `msgpack:"count"`
}

type ormSearchReadInput struct {
	Model  string   `msgpack:"model"`
	Domain string   `msgpack:"domain"`
	Fields []string `msgpack:"fields,omitempty"`
	Order  string   `msgpack:"order,omitempty"`
	Limit  int      `msgpack:"limit,omitempty"`
	Cursor string   `msgpack:"cursor,omitempty"`
}

type ormSearchReadOutput struct {
	Records    []map[string]any `msgpack:"records"`
	NextCursor string           `msgpack:"next_cursor,omitempty"`
}

// searchOpts is Search/SearchRead's shared option state.
type searchOpts struct {
	Order  string
	Limit  int
	Cursor string
}

// SearchOption configures Search/SearchRead.
type SearchOption func(*searchOpts)

// OrderBy orders results by field, ascending unless desc is set.
func OrderBy(field string, desc bool) SearchOption {
	return func(o *searchOpts) {
		o.Order = field
		if desc {
			o.Order += " DESC"
		}
	}
}

// Limit caps the number of results returned.
func Limit(n int) SearchOption {
	return func(o *searchOpts) { o.Limit = n }
}

// Cursor resumes SearchRead from a previous page's next cursor. Has no
// effect on Search — host.orm.search doesn't support cursor pagination.
func Cursor(cursor string) SearchOption {
	return func(o *searchOpts) { o.Cursor = cursor }
}

// Search finds matching record IDs via host.orm.search. domain is the
// domain expression language (manifest-spec.md §8), not raw SQL or a
// model-field struct.
func Search(model, domain string, opts ...SearchOption) ([]string, error) {
	var o searchOpts
	for _, opt := range opts {
		opt(&o)
	}
	var out ormSearchOutput
	err := hostcall.Do(hostORMSearch, ormSearchInput{Model: model, Domain: domain, Order: o.Order, Limit: o.Limit}, &out)
	return out.IDs, err
}

// SearchCount counts matching records via the same host.orm.search call
// Search makes, discarding the ID list.
func SearchCount(model, domain string) (int64, error) {
	var out ormSearchOutput
	err := hostcall.Do(hostORMSearch, ormSearchInput{Model: model, Domain: domain}, &out)
	return out.Count, err
}

// SearchRead finds and reads matching records in one call via
// host.orm.search_read, mapping each into a T via its own db-tag-mapped
// fields. Returns (records, nextCursor, error); nextCursor is "" when
// there are no more pages.
func SearchRead[T any](model, domain string, fields []string, opts ...SearchOption) ([]T, string, error) {
	var o searchOpts
	for _, opt := range opts {
		opt(&o)
	}
	var out ormSearchReadOutput
	in := ormSearchReadInput{Model: model, Domain: domain, Fields: fields, Order: o.Order, Limit: o.Limit, Cursor: o.Cursor}
	if err := hostcall.Do(hostORMSearchRead, in, &out); err != nil {
		return nil, "", err
	}
	records, err := decodeRecords[T](out.Records)
	if err != nil {
		return nil, "", err
	}
	return records, out.NextCursor, nil
}
