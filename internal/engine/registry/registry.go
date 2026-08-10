package registry

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
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

	newSnap := &RegistrySnapshot{
		modules:          modules,
		routeTable:       routeTable,
		eventRegistry:    buildEventRegistry(modules),
		permRegistry:     buildPermissionRegistry(modules),
		fieldSecRegistry: buildFieldSecRegistry(modules),
	}
	if old != nil {
		newSnap.jobRegistry = old.jobRegistry
		newSnap.cronRegistry = old.cronRegistry
		newSnap.schemaRegistry = old.schemaRegistry
	}

	r.current.Store(newSnap)
	return newSnap, nil
}

func buildFieldSecRegistry(modules map[string]*module.LoadedModule) *fieldsec.FieldSecurityRegistry {
	reg := fieldsec.New()
	for name, m := range modules {
		reg.Register(name, m.ModelDecls)
	}
	return reg
}

func buildRouteTable(modules map[string]*module.LoadedModule) (*route.RouteTable, error) {
	table := route.New()
	for name, m := range modules {
		explicit := make([]route.ExplicitRoute, len(m.ExplicitRoutes))
		for i, r := range m.ExplicitRoutes {
			explicit[i] = route.ExplicitRoute{Method: r.Method, Path: r.Path}
		}
		if err := route.RegisterModuleRoutes(table, name, m.Manifest.Type, explicit); err != nil {
			return nil, fmt.Errorf("module %q: %w", name, err)
		}
	}
	return table, nil
}

func buildEventRegistry(modules map[string]*module.LoadedModule) *event.EventRegistry {
	reg := event.NewEventRegistry()
	for name, m := range modules {
		reg.Register(name, m.Manifest)
	}
	return reg
}

func buildPermissionRegistry(modules map[string]*module.LoadedModule) *permission.PermissionRegistry {
	reg := permission.NewPermissionRegistry()
	for name, m := range modules {
		reg.Register(name, m.Manifest.Permissions)
	}
	return reg
}
