// Package searchindex indexes each module's manifest-declared
// search_indexes[] (manifest-spec.md, "search_indexes") into a lookup
// keyed by "{module}.{index name}" — a calling module only ever passes
// search.Query its own bare, self-declared index name
// (host-abi-reference.md §12's own example, `search.Query("contacts",
// ...)`), so the host function resolving that call qualifies it with the
// caller's own ModuleContext.ModuleName before looking it up here, the
// same qualification internal/engine/fieldsec's model-name keys already
// use and for the same reason: two modules may each declare an index
// (or model) of the same bare name without colliding.
//
// This package imports only internal/engine/manifest, a leaf package,
// deliberately, so internal/engine/wasm (which cannot import
// internal/engine/registry or internal/engine/module without an import
// cycle) can still consume the built registry — the same reasoning
// internal/engine/fieldsec/internal/engine/dataaudit already established
// for their own reverse-lookup types.
package searchindex

import "github.com/djangbahevans/goerp/internal/engine/manifest"

// Registry is the search-index lookup — built once per
// registry.ModuleRegistry.Update() (mirroring
// internal/engine/dataaudit.Registry's own build-once-per-snapshot shape)
// and never mutated after Register calls finish.
type Registry struct {
	byQualifiedName map[string]manifest.SearchIndex
}

func New() *Registry {
	return &Registry{byQualifiedName: make(map[string]manifest.SearchIndex)}
}

// Register indexes moduleName's own search_indexes[] declarations, each
// keyed by "moduleName.idx.Name".
func (r *Registry) Register(moduleName string, indexes []manifest.SearchIndex) {
	for _, idx := range indexes {
		r.byQualifiedName[moduleName+"."+idx.Name] = idx
	}
}

// Index returns the declared SearchIndex for moduleName's own indexName,
// and whether one is declared at all.
func (r *Registry) Index(moduleName, indexName string) (manifest.SearchIndex, bool) {
	idx, ok := r.byQualifiedName[moduleName+"."+indexName]
	return idx, ok
}
