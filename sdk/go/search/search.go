// Package search is sdk/go's outbound module-side caller for the
// host.search namespace (host-abi-reference.md §12) — Query, calling
// host.search.query via sdk/go/internal/hostcall. host-abi-reference.md
// §12's own SDK signature is generic (func Query[T any](...)); no
// existing sdk/go/* namespace package (orm, authz, storage) uses
// generics, so this follows their convention instead — a concrete
// QueryOutput with hits as []map[string]any, the same shape
// sdk/go/orm.SearchReadOutput already uses.
//
// Update/Delete aren't offered here — the engine's host.search.update/
// delete always report abi.unavailable against the trigram-only initial
// backend (host-abi-reference.md §12), so there's nothing for a module
// to meaningfully call yet.
package search

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

// QueryOpts configures Query. Filter/Sort/Facets are Meilisearch-only —
// the trigram backend ignores them and FacetDistribution in QueryOutput
// is always empty — until Meilisearch is introduced.
type QueryOpts struct {
	Filter string   `msgpack:"filter,omitempty"`
	Sort   []string `msgpack:"sort,omitempty"`
	Limit  int      `msgpack:"limit,omitempty"`
	Offset int      `msgpack:"offset,omitempty"`
	Facets []string `msgpack:"facets,omitempty"`
}

type QueryInput struct {
	Index string    `msgpack:"index"`
	Query string    `msgpack:"query"`
	Opts  QueryOpts `msgpack:"opts"`
}

type QueryOutput struct {
	Hits              []map[string]any          `msgpack:"hits"`
	TotalHits         int64                     `msgpack:"total_hits"`
	ProcessingTimeMs  int                       `msgpack:"processing_time_ms"`
	FacetDistribution map[string]map[string]int `msgpack:"facet_distribution,omitempty"`
}

// Query searches indexName — one of the calling module's own declared
// search_indexes entries — via host.search.query.
func Query(in QueryInput) (QueryOutput, error) {
	var out QueryOutput
	err := hostcall.Do(hostSearchQuery, in, &out)
	return out, err
}
