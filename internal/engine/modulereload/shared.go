package modulereload

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// currentModules returns reg's live module map, or nil if reg has never
// published a snapshot. Shared by Leader.Run and Follower.Run — both read
// the current state of the same *registry.ModuleRegistry to find an old
// module instance to drain after their own publish.
func currentModules(reg *registry.ModuleRegistry) map[string]*module.LoadedModule {
	snap := reg.Snapshot()
	if snap == nil {
		return nil
	}
	return snap.Modules()
}

// reserveModule claims name for a leader or follower run in progress via
// the registry's own shared Reserve — the same cross-writer-kind
// reservation moduleinstall.Worker also goes through, so a leader run, a
// follower run, and an install of the same module name can never race
// each other on this instance. See errReloadInProgress's own doc comment
// for why this is purely advisory.
func reserveModule(reg *registry.ModuleRegistry, name string) (release func(), err error) {
	release, err = reg.Reserve(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", errReloadInProgress, name)
	}
	return release, nil
}

// Publication and permission-cache rebuilding share a lock so concurrent writers cannot
// publish stale grants. committed stays true if cache rebuilding fails after publication.
func publishModule(ctx context.Context, reg *registry.ModuleRegistry, rolePerms *permcache.RolePermissionMap, tenantStore *tenant.Store, roleStore *role.Store, mod *module.LoadedModule) (committed bool, err error) {
	reg.Lock()
	defer reg.Unlock()

	newSnap, err := reg.UpdateWithLockedContext(ctx, func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		if old := current[mod.Manifest.Name]; old != nil {
			mod.LoadOrder = old.LoadOrder
		} else {
			mod.LoadOrder = len(current)
		}
		return loader.ValidateCandidate(mod, current)
	})
	if err != nil {
		return false, fmt.Errorf("publish module registry: %w", err)
	}

	if err := rolePerms.RebuildAll(ctx, tenantStore, roleStore, newSnap.PermissionRegistry()); err != nil {
		return true, fmt.Errorf("rebuild role permission map: %w", err)
	}

	return true, nil
}
