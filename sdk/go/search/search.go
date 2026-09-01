// Package search is sdk/go's outbound module-side caller for the
// host.search namespace (host-abi-reference.md §12) — Query, calling
// host.search.query via sdk/go/internal/hostcall.
//
// Update/Delete aren't offered here — the engine's host.search.update/
// delete always report abi.unavailable against the trigram-only initial
// backend (host-abi-reference.md §12), so there's nothing for a module
// to meaningfully call yet.
package search

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// searchQueryOpts/searchQueryInput/searchQueryOutput are host.search.query's
// msgpack wire shape. Filter/Sort/Facets are ignored (and
// FacetDistribution always empty) until Meilisearch replaces the initial
// trigram backend.
type searchQueryOpts struct {
	Filter string   `msgpack:"filter,omitempty"`
	Sort   []string `msgpack:"sort,omitempty"`
	Limit  int      `msgpack:"limit,omitempty"`
	Offset int      `msgpack:"offset,omitempty"`
	Facets []string `msgpack:"facets,omitempty"`
}

type searchQueryInput struct {
	Index string          `msgpack:"index"`
	Query string          `msgpack:"query"`
	Opts  searchQueryOpts `msgpack:"opts"`
}

type searchQueryOutput struct {
	Hits              []map[string]any          `msgpack:"hits"`
	TotalHits         int64                     `msgpack:"total_hits"`
	ProcessingTimeMs  int                       `msgpack:"processing_time_ms"`
	FacetDistribution map[string]map[string]int `msgpack:"facet_distribution,omitempty"`
}

// SearchOption configures Query.
type SearchOption func(*searchQueryInput)

// WithFilter sets a Meilisearch filter expression.
func WithFilter(expr string) SearchOption {
	return func(in *searchQueryInput) { in.Opts.Filter = expr }
}

// WithSort orders hits by one or more "field:asc"/"field:desc" clauses.
func WithSort(fields ...string) SearchOption {
	return func(in *searchQueryInput) { in.Opts.Sort = fields }
}

// WithLimit caps the number of hits returned (default 20, max 1000).
func WithLimit(n int) SearchOption {
	return func(in *searchQueryInput) { in.Opts.Limit = n }
}

// WithOffset skips the first n hits, for pagination.
func WithOffset(n int) SearchOption {
	return func(in *searchQueryInput) { in.Opts.Offset = n }
}

// WithFacets requests facet counts for the given fields.
func WithFacets(fields ...string) SearchOption {
	return func(in *searchQueryInput) { in.Opts.Facets = fields }
}

// SearchResult is Query's own result, Hits mapped into T via its own
// json-tag-mapped fields (go-sdk-reference.md §12's ContactSearchHit
// example).
type SearchResult[T any] struct {
	Hits []T
	// TotalHits matches go-sdk-reference.md §12's documented `int` —
	// narrowed from the wire's int64 count, which only loses precision
	// past 2^31 matching rows, unreachable at this system's real scale.
	TotalHits         int
	ProcessingTimeMs  int
	FacetDistribution map[string]map[string]int
	Query             string
}

// Query searches indexName — one of the calling module's own declared
// search_indexes entries — via host.search.query.
func Query[T any](indexName, query string, opts ...SearchOption) (SearchResult[T], error) {
	in := searchQueryInput{Index: indexName, Query: query}
	for _, opt := range opts {
		opt(&in)
	}

	var out searchQueryOutput
	if err := hostcall.Do(hostSearchQuery, in, &out); err != nil {
		return SearchResult[T]{}, err
	}

	hits, err := decodeHits[T](out.Hits)
	if err != nil {
		return SearchResult[T]{}, err
	}

	return SearchResult[T]{
		Hits:              hits,
		TotalHits:         int(out.TotalHits),
		ProcessingTimeMs:  out.ProcessingTimeMs,
		FacetDistribution: out.FacetDistribution,
		Query:             query,
	}, nil
}

// searchHitField pairs one T field with the json key its value maps to —
// this package's map-keyed counterpart to sdk/go/db/reflect.go's
// db-tag-mapped structField, since a hit is a map[string]any rather than
// a positional row.
type searchHitField struct {
	key   string
	index int
}

// searchHitFields returns t's own json-tag-mapped fields (json tags, not
// db.Query[T]'s db tags — go-sdk-reference.md §12's ContactSearchHit
// example), matching encoding/json's own tag semantics: name, "-" to
// skip, untagged falls back to the Go field name as-is.
func searchHitFields(t reflect.Type) ([]searchHitField, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("search: %s is not a struct", t)
	}
	fields := make([]searchHitField, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, ok := f.Tag.Lookup("json")
		name, _, _ := strings.Cut(tag, ",")
		if ok && name == "-" {
			continue
		}
		key := name
		if key == "" {
			key = f.Name
		}
		fields = append(fields, searchHitField{key: key, index: i})
	}
	return fields, nil
}

// decodeHits maps each hit into a T via one reflective pass over its own
// json-tag-mapped fields — hits have already paid for one full
// deserialization in hostcall.Do's own msgpack.Unmarshal, so this avoids
// re-serializing them to JSON text just to parse that text back out.
func decodeHits[T any](hits []map[string]any) ([]T, error) {
	if len(hits) == 0 {
		return nil, nil
	}
	fields, err := searchHitFields(reflect.TypeFor[T]())
	if err != nil {
		return nil, err
	}
	out := make([]T, len(hits))
	for i, hit := range hits {
		v := reflect.ValueOf(&out[i]).Elem()
		for _, f := range fields {
			raw, ok := hit[f.key]
			if !ok {
				continue
			}
			if err := setHitFieldValue(v.Field(f.index), raw); err != nil {
				return nil, fmt.Errorf("search: hit %d field %q: %w", i, f.key, err)
			}
		}
	}
	return out, nil
}

// setHitFieldValue assigns raw (one value from a msgpack-decoded hit)
// into field — sdk/go/db/reflect.go's own setFieldValue, duplicated
// rather than imported (sdk/go/orm/hostcall.go's own stated precedent):
// both packages decode the same msgpack-native Go value shapes off host
// responses, so the same nil/pointer/numeric-conversion rules apply.
func setHitFieldValue(field reflect.Value, raw any) error {
	if raw == nil {
		if field.Kind() != reflect.Pointer {
			return fmt.Errorf("cannot assign NULL into non-pointer field type %s", field.Type())
		}
		return nil
	}
	if field.Kind() == reflect.Pointer {
		elem := reflect.New(field.Type().Elem())
		if err := setHitFieldValue(elem.Elem(), raw); err != nil {
			return err
		}
		field.Set(elem)
		return nil
	}

	rv := reflect.ValueOf(raw)
	if rv.Type().AssignableTo(field.Type()) {
		field.Set(rv)
		return nil
	}
	if rv.Type().ConvertibleTo(field.Type()) {
		switch field.Kind() {
		case reflect.String, reflect.Bool, reflect.Struct:
			// A ConvertibleTo pass for these kinds is almost always a
			// coincidental method-set match, not a real, intended
			// conversion — restrict conversion to the numeric kinds it's
			// actually meant for.
		default:
			field.Set(rv.Convert(field.Type()))
			return nil
		}
	}
	return fmt.Errorf("cannot assign %s into %s", rv.Type(), field.Type())
}
