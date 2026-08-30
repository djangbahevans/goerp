package modulereload

import (
	"context"
	"fmt"
	"maps"

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

// publishModule merges mod into reg's current module map and rebuilds the
// permission cache that has to stay in lockstep with it. Shared by
// Leader.Run and Follower.Run, both of which need the identical lock-span
// contract: reg.Lock held across both UpdateWithLocked and
// rolePerms.RebuildAll, so a leader publishing one module and a follower
// publishing another can't have their RebuildAll calls interleave and let
// whichever's slower DB queries finish last silently overwrite the
// other's more current permission cache, regardless of which one actually
// published last.
//
// committed reports whether reg.UpdateWithLocked itself succeeded,
// independent of err: a RebuildAll failure after a successful
// UpdateWithLocked still returns committed=true, because mod is already
// live and reachable through the registry snapshot at that point — the
// caller's own cleanup defer must not close mod's pool out from under a
// module the registry now routes traffic to, even though this call is
// still reporting an error (a stale permission cache, real but a lesser
// problem than closing a live module's pool).
func publishModule(ctx context.Context, reg *registry.ModuleRegistry, rolePerms *permcache.RolePermissionMap, tenantStore *tenant.Store, roleStore *role.Store, mod *module.LoadedModule) (committed bool, err error) {
	reg.Lock()
	defer reg.Unlock()

	newSnap, err := reg.UpdateWithLocked(func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		merged := maps.Clone(current)
		if merged == nil {
			merged = make(map[string]*module.LoadedModule, 1)
		}
		merged[mod.Manifest.Name] = mod
		return merged, nil
	})
	if err != nil {
		return false, fmt.Errorf("publish module registry: %w", err)
	}

	if err := rolePerms.RebuildAll(ctx, tenantStore, roleStore, newSnap.PermissionRegistry()); err != nil {
		return true, fmt.Errorf("rebuild role permission map: %w", err)
	}

	return true, nil
}
