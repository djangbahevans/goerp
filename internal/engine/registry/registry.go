package registry

import (
	"errors"
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

// ErrReserved is Reserve's rejection when another writer already holds
// name — install, hot reload, or any other writer, whichever got there
// first.
var ErrReserved = errors.New("module name is reserved by another writer")

type ModuleRegistry struct {
	current atomic.Pointer[RegistrySnapshot]
	writeMu sync.Mutex

	reserveMu sync.Mutex
	reserved  map[string]struct{}
}

func (r *ModuleRegistry) Snapshot() *RegistrySnapshot {
	return r.current.Load()
}

// Update replaces the registry's whole module map in one atomic publish.
// Safe against another concurrent Update racing on writeMu, but NOT safe
// against a caller that built modules from a Snapshot() read before
// acquiring any lock — two callers doing read-merge-Update independently
// (not via UpdateWith) can still have the second overwrite the first's
// change with a map built from a now-stale snapshot. Startup's own
// single-threaded bulk load is the only caller that still uses this form
// directly; every writer that runs concurrently with other writers
// (install, hot reload) must use UpdateWith instead.
func (r *ModuleRegistry) Update(modules map[string]*module.LoadedModule) (*RegistrySnapshot, error) {
	return r.UpdateWith(func(map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		return modules, nil
	})
}

// UpdateWith locks writeMu, runs UpdateWithLocked, and unlocks — for a
// caller with no other locked work to do around the publish itself. A
// caller that also needs some further step (e.g. rebuilding a derived
// cache from the just-published snapshot) to be atomic with the publish —
// not just internally consistent, but also not interleaved with a second
// writer's own publish+step pair — must instead call Lock, then
// UpdateWithLocked, do that step, then Unlock; see Reserve's own doc
// comment for why two writer kinds racing for the same publish+step pair
// is a real scenario here, not a hypothetical one.
func (r *ModuleRegistry) UpdateWith(mutate func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error)) (*RegistrySnapshot, error) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return r.UpdateWithLocked(mutate)
}

// Lock and Unlock guard writeMu directly, for a caller that needs to hold
// it across UpdateWithLocked plus some further locked step — see
// UpdateWith's own doc comment for when that's actually needed instead of
// just calling UpdateWith.
func (r *ModuleRegistry) Lock()   { r.writeMu.Lock() }
func (r *ModuleRegistry) Unlock() { r.writeMu.Unlock() }

// UpdateWithLocked is UpdateWith's body, callable only while the caller
// already holds writeMu (via Lock) — see UpdateWith's own doc comment for
// why a caller would reach for this instead. Handed the exact module map
// the registry currently publishes (nil on the very first call) so mutate
// can read-then-merge without any gap a concurrent writer could land in
// between — restoring, across the shared *ModuleRegistry every writer
// (install, hot reload, and any future enable/disable/uninstall) holds a
// reference to, the "held for every writer" guarantee engine-internals.md
// §4 describes, which two independent per-writer-kind mutexes (one for
// install, one for hot reload) can't provide on their own: each only
// serializes writers of its own kind against each other, not against the
// other kind, so an install and a hot reload publishing concurrently could
// otherwise each build from the same pre-either-update snapshot and have
// the second silently clobber the first's change. mutate returning an
// error aborts before anything is built or published — e.g. Worker.publish's
// own "already loaded" recheck, safe to run here since it sees the exact
// map this call will publish against, not a stale one.
func (r *ModuleRegistry) UpdateWithLocked(mutate func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error)) (*RegistrySnapshot, error) {
	old := r.current.Load() // may be nil on the very first call
	var currentModules map[string]*module.LoadedModule
	if old != nil {
		currentModules = old.modules
	}

	modules, err := mutate(currentModules)
	if err != nil {
		return nil, err
	}

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

// Reserve claims name for a writer's whole compile/sync/publish sequence —
// not just the final UpdateWith call — before any of that expensive work
// starts, so two different writer kinds (install, hot reload) racing for
// the same module name fail fast against each other instead of each
// running a full pipeline only to have UpdateWith's own mutate closure
// reject the loser at the very end. This matters beyond wasted work: a
// module install and a hot reload of the same module both proceeding past
// their own reservation stage would run concurrent, uncoordinated tenant
// schema sync (DDL) against the same tenant schema, and would each try to
// spawn/respawn the same workflow-worker process independently — Reserve
// is what makes only one of them ever reach that point. Advisory only:
// released before the writer's own UpdateWith call (see each writer's
// publish for why), so UpdateWith's own mutate-time recheck is what
// remains authoritative for the actual publish.
func (r *ModuleRegistry) Reserve(name string) (release func(), err error) {
	r.reserveMu.Lock()
	defer r.reserveMu.Unlock()

	if _, ok := r.reserved[name]; ok {
		return nil, fmt.Errorf("%w: %q", ErrReserved, name)
	}
	if r.reserved == nil {
		r.reserved = make(map[string]struct{})
	}
	r.reserved[name] = struct{}{}

	return func() {
		r.reserveMu.Lock()
		delete(r.reserved, name)
		r.reserveMu.Unlock()
	}, nil
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
		suppressed, err := route.RegisterRoutes(table, name, m.Manifest.Type, explicit, m.ModelDecls)
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
// /auth/mfa/reverify, /admin/users/{id}/mfa/reset, /_meta/permissions,
// and /_meta/shares resolve through the exact same RouteTable.Lookup
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
// /_meta/permissions and /_meta/shares are deliberately not
// EngineBuiltin, unlike every other route registered here —
// auth-internals.md §9 classifies them Class A (the default for "every
// other route"), so they need the standard tenant/auth/permission
// middleware chain to populate authFromContext/tenantFromContext,
// rather than resolving their own identity the way the true
// bootstrap/anonymous routes above do.
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

	// /_meta/shares (goerp#475) — same not-EngineBuiltin posture as
	// /_meta/permissions above, for the same reason: it rides the
	// standard tenant/auth/permission middleware chain rather than
	// resolving its own identity.
	table.Register("POST", "/_meta/shares", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/shares",
	})
	table.Register("GET", "/_meta/shares", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/shares",
	})
	table.Register("DELETE", "/_meta/shares/{id}", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true,
			Auth:         "required",
			PathParams:   map[string]string{"id": "uuid"},
		},
		PathTemplate: "/_meta/shares/{id}",
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
