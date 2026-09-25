// Package dataaudit indexes each module's manifest-declared
// audited_tables[] (manifest-spec.md §19 "Audited Tables") into a reverse
// lookup: given a qualified model that host.orm just created, wrote, or
// unlinked, is its table audited, and if so, which columns are excluded
// from the JSONB old/new-value snapshots written to the per-tenant
// audit_log table.
//
// This package imports only sdk/go/model and internal/engine/manifest —
// both leaf packages — deliberately, so internal/engine/wasm (which
// cannot import internal/engine/registry or internal/engine/module
// without an import cycle, module.LoadedModule itself holding a
// *wasm.InstancePool) can still consume the built registry, the same
// reasoning internal/engine/computed and internal/engine/fieldsec already
// established for their own reverse-lookup types.
package dataaudit

import (
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type tableAudit struct {
	ExcludeColumns map[string]bool
}

// Registry is the reverse-dependency lookup — built once per
// registry.ModuleRegistry.Update() (mirroring
// internal/engine/computed.Index and internal/engine/fieldsec's own
// build-once-per-snapshot shape) and never mutated after Register calls
// finish.
type Registry struct {
	byModel map[string]tableAudit
}

func New() *Registry {
	return &Registry{byModel: make(map[string]tableAudit)}
}

// Register indexes moduleName's own audited_tables[] declarations
// against decls (all owned by moduleName) — resolving each entry's
// table name to the declared model whose own table name matches it. An
// audited-table entry that names a table no declared model owns is
// silently skipped rather than erroring: this package has no load-time
// validation authority (that belongs to internal/engine/loader, a
// separate concern), matching internal/engine/computed.Index.Register's
// own stance on unresolved declarations.
func (r *Registry) Register(moduleName string, audited []manifest.AuditedTable, decls []model.ModelDeclaration) {
	if len(audited) == 0 {
		return
	}

	byTable := make(map[string]manifest.AuditedTable, len(audited))
	for _, a := range audited {
		byTable[a.Table] = a
	}

	for _, decl := range decls {
		a, ok := byTable[modeltable.Name(decl)]
		if !ok {
			continue
		}
		excludeCols := make(map[string]bool, len(a.ExcludeColumns))
		for _, c := range a.ExcludeColumns {
			excludeCols[c] = true
		}
		r.byModel[decl.QualifiedName(moduleName)] = tableAudit{ExcludeColumns: excludeCols}
	}
}

// Lookup reports whether qualifiedModel's table is audited and, if so,
// which columns to exclude from its old/new-value JSONB snapshots.
func (r *Registry) Lookup(qualifiedModel string) (excludeColumns map[string]bool, audited bool) {
	a, ok := r.byModel[qualifiedModel]
	if !ok {
		return nil, false
	}
	return a.ExcludeColumns, true
}
