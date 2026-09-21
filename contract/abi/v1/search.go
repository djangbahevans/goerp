package abi

// SearchQueryOpts holds the options of host.search.query. Filter, Sort and
// Facets are ignored, and FacetDistribution is always empty, on a backend
// without Meilisearch.
type SearchQueryOpts struct {
	Filter string   `msgpack:"filter,omitempty"`
	Sort   []string `msgpack:"sort,omitempty"`
	Limit  int      `msgpack:"limit,omitempty"`
	Offset int      `msgpack:"offset,omitempty"`
	Facets []string `msgpack:"facets,omitempty"`
}

// SearchQueryInput is the request of host.search.query.
type SearchQueryInput struct {
	Index string          `msgpack:"index"`
	Query string          `msgpack:"query"`
	Opts  SearchQueryOpts `msgpack:"opts"`
}

// SearchQueryOutput is the response of host.search.query.
type SearchQueryOutput struct {
	Hits              []map[string]any          `msgpack:"hits"`
	TotalHits         int64                     `msgpack:"total_hits"`
	ProcessingTimeMs  int                       `msgpack:"processing_time_ms"`
	FacetDistribution map[string]map[string]int `msgpack:"facet_distribution,omitempty"`
}
