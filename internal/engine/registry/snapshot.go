package registry

import (
	"net/url"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/dataaudit"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/job"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/searchindex"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type RegistrySnapshot struct {
	modules          map[string]*module.LoadedModule
	schemaHash       string
	schemaResponse   *SchemaResponse
	routeTable       *route.RouteTable
	eventRegistry    *event.EventRegistry
	permRegistry     *permission.PermissionRegistry
	fieldSecRegistry *fieldsec.FieldSecurityRegistry
	searchIndexReg   *searchindex.Registry
	jobRegistry      *job.JobRegistry
	cronRegistry     *CronRegistry
	schemaRegistry   *SchemaRegistry
	computedIndex    *computed.Index
	dataAuditReg     *dataaudit.Registry
	modelsByTable    map[string]string
}

// Modules returns this snapshot's backing map, successful and
// StatusFailed alike. Callers must treat it as read-only.
func (s *RegistrySnapshot) Modules() map[string]*module.LoadedModule {
	return s.modules
}

// SchemaHash returns this snapshot's content hash — GET /_meta/schema's
// (goerp#573) top-level "schema_hash" field, a cheap staleness marker for
// goerp codegen --watch and the shell to detect a schema change without
// diffing the full response. Changes if and only if a route, view, or
// navigation declaration changed (computeSchemaHash).
func (s *RegistrySnapshot) SchemaHash() string {
	return s.schemaHash
}

// SchemaResponse returns this snapshot's precomputed GET /_meta/schema
// response body (goerp#591) — built once per publish (buildSchemaResponse,
// called from UpdateWithLocked) rather than rebuilt on every request.
func (s *RegistrySnapshot) SchemaResponse() *SchemaResponse {
	return s.schemaResponse
}

// RecordFormPath is the browser path of recordID's form view — the GET
// route that serves modelName's (qualified) form view, with the record's
// ID in place of {id}, under the shell's /_m prefix — or "" when the model
// has no form view to open. It resolves the path the shell's record links
// do (resolveRecordViewPath).
func (s *RegistrySnapshot) RecordFormPath(modelName, recordID string) string {
	if s.schemaResponse == nil {
		return ""
	}
	moduleName, _, _ := strings.Cut(modelName, ".")
	mod := s.schemaResponse.Modules[moduleName]
	if mod == nil {
		return ""
	}
	for _, rt := range mod.Routes {
		if rt.Method == "GET" && rt.Model == modelName && rt.CrudAction == "get" && rt.View != "" && strings.Contains(rt.Path, "{id}") {
			return "/_m" + strings.Replace(rt.Path, "{id}", url.PathEscape(recordID), 1)
		}
	}
	return ""
}

// RouteTable returns this snapshot's route table — module-declared routes
// and engine built-ins (/_health, /_ready) alike, the single router the
// HTTP server's dispatch handler consults for every request.
func (s *RegistrySnapshot) RouteTable() *route.RouteTable {
	return s.routeTable
}

// PermissionRegistry returns this snapshot's permission registry — the
// current process's live permission-name-to-bitfield-index assignments
// (permcache.RolePermissionMap.RebuildAll needs these to resolve each
// role's bitfield against this process's own indices).
func (s *RegistrySnapshot) PermissionRegistry() *permission.PermissionRegistry {
	return s.permRegistry
}

// FieldSecRegistry returns this snapshot's field security registry —
// always read this way rather than cached on a longer-lived struct, so a
// hot reload's rebuilt registry takes effect on the next request rather
// than never (see buildFieldSecRegistry).
func (s *RegistrySnapshot) FieldSecRegistry() *fieldsec.FieldSecurityRegistry {
	return s.fieldSecRegistry
}

// SearchIndexRegistry returns this snapshot's search-index lookup — every
// loaded module's declared search_indexes[], keyed by "{module}.{name}".
func (s *RegistrySnapshot) SearchIndexRegistry() *searchindex.Registry {
	return s.searchIndexReg
}

// EventRegistry returns this snapshot's event registry.
func (s *RegistrySnapshot) EventRegistry() *event.EventRegistry {
	return s.eventRegistry
}

// JobRegistry returns this snapshot's job type registry — which module
// declared a given job_types[].name (goerp#110's jobdispatch.Worker uses
// this to confirm a dispatched job's declared JobType is actually owned
// by its declared ModuleName before invoking that module's handle_job
// export).
func (s *RegistrySnapshot) JobRegistry() *job.JobRegistry {
	return s.jobRegistry
}

// ComputedIndex returns this snapshot's computed-field reverse-dependency
// index (go-sdk-reference.md §22 "Computed field recomputation") — which
// fields elsewhere need to recompute when a given model's field changes.
func (s *RegistrySnapshot) ComputedIndex() *computed.Index {
	return s.computedIndex
}

// DataAuditRegistry returns this snapshot's audited-tables reverse lookup
// (manifest-spec.md §19 "Audited Tables") — which qualified models
// host.orm's write path must record an audit_log row for, and which
// columns to exclude from the JSONB old/new-value snapshots.
func (s *RegistrySnapshot) DataAuditRegistry() *dataaudit.Registry {
	return s.dataAuditReg
}

// ModelByName resolves a module-qualified "{module}.{resource}" model name
// (RouteManifest.Model's shape) back to the owning module and its
// ModelDeclaration. A module that failed to load is never
// matched, mirroring buildRouteTable's own StatusFailed skip.
func (s *RegistrySnapshot) ModelByName(qualified string) (moduleName string, mod *module.LoadedModule, md model.ModelDeclaration, ok bool) {
	moduleName, _, found := strings.Cut(qualified, ".")
	if !found {
		return "", nil, model.ModelDeclaration{}, false
	}

	mod, exists := s.modules[moduleName]
	if !exists || mod.Status == module.StatusFailed {
		return "", nil, model.ModelDeclaration{}, false
	}

	for _, decl := range mod.ModelDecls {
		if decl.QualifiedName(moduleName) == qualified {
			return moduleName, mod, decl, true
		}
	}
	return "", nil, model.ModelDeclaration{}, false
}

// ModelForTable returns the qualified name of the loaded model whose table
// is table, the mapping audit_log.table_name needs.
func (s *RegistrySnapshot) ModelForTable(table string) (string, bool) {
	qualified, ok := s.modelsByTable[table]
	return qualified, ok
}

// ComputeTargets captures each module's pool and declarations for nested calls.
// The registry owns this lookup because importing it from wasm would create a cycle.
func ComputeTargets(snap *RegistrySnapshot) map[string]wasm.ComputeTarget {
	targets := make(map[string]wasm.ComputeTarget, len(snap.modules))
	for name, m := range snap.modules {
		if m.Status == module.StatusFailed {
			continue
		}
		targets[name] = wasm.ComputeTarget{
			Pool:          m.Pool,
			Capabilities:  m.Capabilities,
			ModelDecls:    m.ModelDecls,
			ConfigSchema:  m.Manifest.ConfigSchema,
			UsesConfig:    m.UsesConfig,
			JobTypes:      m.Manifest.JobTypes,
			HTTPAllowlist: m.Manifest.HTTPAllowlist,
		}
	}

	return targets
}

// Populated by future tickets (backlog #35, #37). Never rebuilt by any
// build* step here; carried over unchanged from the prior snapshot on
// every write.
type CronRegistry struct{}
type SchemaRegistry struct{}
