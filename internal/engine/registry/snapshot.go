package registry

import (
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/dataaudit"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/job"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/searchindex"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type RegistrySnapshot struct {
	modules          map[string]*module.LoadedModule
	builtAt          time.Time
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
}

// Modules returns this snapshot's backing map, successful and
// StatusFailed alike. Callers must treat it as read-only.
func (s *RegistrySnapshot) Modules() map[string]*module.LoadedModule {
	return s.modules
}

// BuiltAt returns when this snapshot was published — /_meta/schema's
// (goerp#573) top-level "version" timestamp, a cheap staleness marker for
// goerp codegen --watch and the shell to detect a schema change without
// diffing the full response.
func (s *RegistrySnapshot) BuiltAt() time.Time {
	return s.builtAt
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

// NotifTemplate resolves moduleName's notificationType/channel template
// for userLocale (notiftemplate.ModuleTemplates.Resolve's 3-step
// fallback), returning ok=false if the module doesn't exist, failed to
// load, declares no matching template, or none of the fallback locales
// match — the same "logged here, caller decides whether to skip" contract
// Resolve itself has.
func (s *RegistrySnapshot) NotifTemplate(moduleName, notificationType, channel, userLocale string) (locale string, tmpl *notiftemplate.Template, ok bool) {
	mod, exists := s.modules[moduleName]
	if !exists || mod.Status == module.StatusFailed {
		return "", nil, false
	}
	return mod.NotifTemplates.Resolve(notificationType, channel, userLocale)
}

// ModelByName resolves a RouteManifest.Model-shaped "{module}.{resource}"
// string (route.RegisterModelRoutes's own qualifiedModel convention —
// moduleName + "." + the model's bare, undotted Name) back to the owning
// module and its ModelDeclaration. A module that failed to load is never
// matched, mirroring buildRouteTable's own StatusFailed skip.
func (s *RegistrySnapshot) ModelByName(qualified string) (moduleName string, mod *module.LoadedModule, md model.ModelDeclaration, ok bool) {
	moduleName, resource, found := strings.Cut(qualified, ".")
	if !found {
		return "", nil, model.ModelDeclaration{}, false
	}

	mod, exists := s.modules[moduleName]
	if !exists || mod.Status == module.StatusFailed {
		return "", nil, model.ModelDeclaration{}, false
	}

	for _, decl := range mod.ModelDecls {
		if decl.Name == resource {
			return moduleName, mod, decl, true
		}
	}
	return "", nil, model.ModelDeclaration{}, false
}

// ComputeTargets builds one wasm.ComputeTarget per loaded, non-failed
// module in snap — the per-request data host.orm's write/read halves need
// to borrow a fresh WASM instance from any module and invoke its
// .Computed() functions (go-sdk-reference.md §22 "Computed field
// recomputation"), regardless of which module's own write or read
// triggered the recompute. Lives here (not in wasm) because it needs
// module.LoadedModule's Pool/Capabilities/ModelDecls, and wasm cannot
// import module without an import cycle (module.LoadedModule itself holds
// a *wasm.InstancePool).
func ComputeTargets(snap *RegistrySnapshot) map[string]wasm.ComputeTarget {
	targets := make(map[string]wasm.ComputeTarget, len(snap.modules))
	for name, m := range snap.modules {
		if m.Status == module.StatusFailed {
			continue
		}
		targets[name] = wasm.ComputeTarget{
			Pool:         m.Pool,
			Capabilities: m.Capabilities,
			ModelDecls:   m.ModelDecls,
		}
	}
	return targets
}

// Populated by future tickets (backlog #35, #37). Never rebuilt by any
// build* step here; carried over unchanged from the prior snapshot on
// every write.
type CronRegistry struct{}
type SchemaRegistry struct{}
