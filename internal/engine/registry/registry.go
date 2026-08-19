package registry

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/job"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/route"
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
		jobRegistry:      jobRegistry,
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
	}
	return table, nil
}

// registerBuiltinRoutes registers the engine's own built-in routes into
// table, so /_health and /_ready resolve through the exact same
// RouteTable.Lookup module routes do — no second router. Safe against
// collision by construction: RegisterModuleRoutes already rejects any
// module route whose top path segment starts with "_" as a reserved
// engine namespace.
func registerBuiltinRoutes(table *route.RouteTable) {
	for _, path := range []string{"/_health", "/_ready"} {
		table.Register("GET", path, &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true},
			PathTemplate: path,
		})
	}
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
