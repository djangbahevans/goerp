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
	"encoding/json/v2"
	"fmt"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// searchQueryOpts/searchQueryInput/searchQueryOutput are host.search.query's
// own msgpack wire shape. Filter/Sort/Facets are Meilisearch-only — the
// trigram backend ignores them and FacetDistribution in searchQueryOutput
// is always empty — until Meilisearch is introduced.
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

// SearchResult is Query's own result — Hits already mapped into T via its
// own json-tag-mapped fields, matching go-sdk-reference.md §12's own
// ContactSearchHit example.
type SearchResult[T any] struct {
	Hits              []T
	TotalHits         int
	ProcessingTimeMs  int
	FacetDistribution map[string]map[string]int
	Query             string
}

// Query searches indexName — one of the calling module's own declared
// search_indexes entries — via host.search.query, populating each hit
// into a T via its own json-tag-mapped fields. A hit arrives as an
// already-decoded map[string]any keyed by column name, so a marshal/
// unmarshal round trip through T's own json tags is simpler here than
// duplicating sdk/go/db/reflect.go's positional row-scanning machinery,
// which solves a different problem (a []any row paired with a parallel
// []string of column names, not a map).
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

// decodeHits maps each hit into a T via its own json-tag-mapped fields.
func decodeHits[T any](hits []map[string]any) ([]T, error) {
	b, err := json.Marshal(hits)
	if err != nil {
		return nil, fmt.Errorf("search: marshal hits: %w", err)
	}
	var out []T
	if err := json.Unmarshal(b, &out); err != nil {
		var zero T
		return nil, fmt.Errorf("search: unmarshal hits into []%T: %w", zero, err)
	}
	return out, nil
}
