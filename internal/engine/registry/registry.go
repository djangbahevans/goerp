package registry

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/dataaudit"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/job"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/searchindex"
	"github.com/rs/zerolog/log"
)

type ModuleRegistry struct {
	current atomic.Pointer[RegistrySnapshot]
	writeMu sync.Mutex
}

func (r *ModuleRegistry) Snapshot() *RegistrySnapshot {
	return r.current.Load()
}

func (r *ModuleRegistry) Update(modules map[string]*module.LoadedModule) (*RegistrySnapshot, error) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	old := r.current.Load() // may be nil on the very first call

	validateSyncSubscriptionCycles(modules)

	routeTable, err := buildRouteTable(modules)
	if err != nil {
		return nil, fmt.Errorf("build route table: %w", err)
	}

	jobRegistry, err := buildJobRegistry(modules)
	if err != nil {
		return nil, fmt.Errorf("build job registry: %w", err)
	}

	newSnap := &RegistrySnapshot{
		modules:          modules,
		routeTable:       routeTable,
		eventRegistry:    buildEventRegistry(modules),
		permRegistry:     buildPermissionRegistry(modules),
		fieldSecRegistry: buildFieldSecRegistry(modules),
		searchIndexReg:   buildSearchIndexRegistry(modules),
		jobRegistry:      jobRegistry,
		computedIndex:    buildComputedIndex(modules),
		dataAuditReg:     buildDataAuditRegistry(modules),
	}
	if old != nil {
		newSnap.cronRegistry = old.cronRegistry
		newSnap.schemaRegistry = old.schemaRegistry
	}

	r.current.Store(newSnap)
	return newSnap, nil
}

// The four build* functions below all skip StatusFailed modules — a
// module that never finished loading shouldn't claim a route, emit an
// event, or expose a permission, and skipping in buildRouteTable avoids
// re-triggering a route conflict the caller already resolved by failing
// that module before calling Update.

func buildFieldSecRegistry(modules map[string]*module.LoadedModule) *fieldsec.FieldSecurityRegistry {
	reg := fieldsec.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.ModelDecls)
	}
	return reg
}

func buildSearchIndexRegistry(modules map[string]*module.LoadedModule) *searchindex.Registry {
	reg := searchindex.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.Manifest.SearchIndexes)
	}
	return reg
}

func buildComputedIndex(modules map[string]*module.LoadedModule) *computed.Index {
	idx := computed.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		idx.Register(name, m.ModelDecls)
	}
	return idx
}

func buildDataAuditRegistry(modules map[string]*module.LoadedModule) *dataaudit.Registry {
	reg := dataaudit.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.Manifest.AuditedTables, m.ModelDecls)
	}
	return reg
}

func buildRouteTable(modules map[string]*module.LoadedModule) (*route.RouteTable, error) {
	table := route.New()
	registerBuiltinRoutes(table)
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		explicit := route.ExplicitRoutesFrom(m.ExplicitRoutes)
		if err := route.RegisterModuleRoutes(table, name, m.Manifest.Type, explicit); err != nil {
			return nil, fmt.Errorf("module %q: %w", name, err)
		}
		suppressed, err := route.RegisterModelRoutes(table, name, m.Manifest.Type, m.ModelDecls)
		if err != nil {
			return nil, fmt.Errorf("module %q: %w", name, err)
		}
		for _, s := range suppressed {
			log.Warn().Str("module", name).Str("model", s.Model).Str("op", s.Op).
				Msg("EnableOps: explicit route already registered, auto-derived route suppressed")
		}
	}
	return table, nil
}

// registerBuiltinRoutes registers the engine's own built-in routes into
// table, so /_health, /_ready, /auth/login, /auth/mfa/verify,
// /auth/mfa/reverify, /admin/users/{id}/mfa/reset, and
// /_meta/permissions resolve through the exact same RouteTable.Lookup
// module routes do — no second router. Safe against collision by
// construction: RegisterModuleRoutes already rejects any module route
// whose top path segment starts with "_", or is exactly "auth" or
// "admin", as a reserved engine namespace.
//
// /admin/users/{id}/mfa/reset is a tenant-facing route despite its
// "/admin/" prefix — see internal/engine/auth/mfareset's own package doc
// for why that prefix doesn't mean it belongs to the separate
// internal/engine/adminapi operator surface.
//
// /_meta/permissions is deliberately not EngineBuiltin, unlike every
// other route registered here — auth-internals.md §9 classifies it Class
// A (the default for "every other route"), so it needs the standard
// tenant/auth/permission middleware chain to populate authFromContext/
// tenantFromContext for it, rather than resolving its own identity the
// way the true bootstrap/anonymous routes above do.
func registerBuiltinRoutes(table *route.RouteTable) {
	for _, path := range []string{"/_health", "/_ready"} {
		table.Register("GET", path, &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
			PathTemplate: path,
		})
	}
	for _, path := range []string{"/auth/login", "/auth/mfa/verify", "/auth/mfa/reverify", "/admin/users/{id}/mfa/reset"} {
		table.Register("POST", path, &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
			PathTemplate: path,
		})
	}
	table.Register("GET", "/_meta/permissions", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/permissions",
	})
}

func buildEventRegistry(modules map[string]*module.LoadedModule) *event.EventRegistry {
	reg := event.NewEventRegistry()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.Manifest)
	}
	return reg
}

func buildPermissionRegistry(modules map[string]*module.LoadedModule) *permission.PermissionRegistry {
	reg := permission.NewPermissionRegistry()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.Manifest.Permissions)
	}
	return reg
}

// buildJobRegistry, unlike buildEventRegistry/buildPermissionRegistry/
// buildFieldSecRegistry, can fail — job_types[].name, like a route, must be
// unique across all loaded modules, not just within one. By the time this
// runs, loader.LoadAll's own incremental pass has already marked
// StatusFailed on whichever module lost a name collision (skipped by the
// range below), so this full rebuild should not normally hit one itself;
// it returns an error rather than panicking if it ever does.
func buildJobRegistry(modules map[string]*module.LoadedModule) (*job.JobRegistry, error) {
	reg := job.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		if err := reg.Register(name, m.Manifest.JobTypes); err != nil {
			return nil, fmt.Errorf("module %q: %w", name, err)
		}
	}
	return reg, nil
}
