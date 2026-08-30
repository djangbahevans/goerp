package modulereload

import (
	"context"
	"fmt"
	"io"
	"maps"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/workflowworker"
	"github.com/rs/zerolog/log"
)

// Follower implements hotreload.FollowerFunc via Run: the sequence a
// hot-reload follower instance runs once it learns (via the
// engine:reload:{module} announcement) that another instance has already
// led a reload for (module, version) — adopt the leader-published binary
// without repeating the schema sync the leader already ran against the
// shared Postgres every instance points at.
//
// Follower is a narrow, independently constructible struct rather than a
// method on *Engine, the same pattern Leader itself and
// moduleinstall.Worker already use. It shares every primitive Leader.Run
// uses except SyncPool/DiffEngine/Cache — a follower never syncs schema
// and never publishes its own reload announcement, so it needs none of
// them.
type Follower struct {
	Runtime     *wasm.Runtime
	PoolCfg     wasm.PoolConfig
	Registry    *registry.ModuleRegistry
	RolePerms   *permcache.RolePermissionMap
	TenantStore *tenant.Store
	RoleStore   *role.Store
	Storage     storage.Backend
	Workers     *workflowworker.Manager
}

// Run implements hotreload.FollowerFunc. Coordinator only invokes this
// once this instance has confirmed (via CurrentVersionAtLeast) it hasn't
// already adopted version or newer, so Run itself never re-checks that.
func (f *Follower) Run(ctx context.Context, moduleName, version, objectKey string) error {
	// Object storage is warn-only at Engine startup (engine-internals.md
	// §2), so f.Storage can reach here nil even on an otherwise healthy
	// engine — checked up front, same as Leader.Run, so a follower attempt
	// fails fast with a clear error instead of a nil-pointer panic partway
	// through a download.
	if f.Storage == nil {
		return fmt.Errorf("object storage unavailable")
	}

	release, err := f.reserve(moduleName)
	if err != nil {
		return err
	}
	releaseOnce := sync.OnceFunc(release)
	defer releaseOnce()

	wasmBytes, err := f.download(ctx, objectKey)
	if err != nil {
		return fmt.Errorf("download published binary: %w", err)
	}
	manifestBytes, err := f.download(ctx, objectKey+".manifest.json")
	if err != nil {
		return fmt.Errorf("download published manifest: %w", err)
	}

	// loader.LoadModule re-verifies the checksum against manifestBytes'
	// own Checksum field — the defense-in-depth against a corrupted or
	// tampered download this ticket's own acceptance criteria call for.
	// It also compiles, instantiates a temporary instance to fetch
	// get_routes/get_model_declarations/get_data_migrations, and creates
	// the new instance pool, stopping at StatusSyncing — there is no
	// separate schema-sync step to run here: the leader already ran it
	// once, against the same shared Postgres every instance points at.
	mod := loader.LoadModule(ctx, f.Runtime, f.PoolCfg, loader.Source{
		Name:          moduleName,
		ManifestBytes: manifestBytes,
		WasmBytes:     wasmBytes,
	})
	if mod.Status == module.StatusFailed {
		return fmt.Errorf("load module: %s", mod.FailureReason)
	}

	// From here on, mod owns a live pool and compiled module. published
	// tracks whether mod made it into the registry; if any step below
	// fails first, mod is never reachable through the registry, so
	// nothing else will ever close it.
	published := false
	defer func() {
		if !published {
			mod.Pool.DrainAndClose(context.Background(), 5*time.Second)
			_ = mod.CompiledModule.Close(context.Background())
		}
	}()

	oldMod := f.currentModules()[moduleName]
	mod.Status = module.StatusReady

	// The reservation's job is done; release it before publish acquires
	// its own narrower lock, matching Leader.Run's identical ordering.
	releaseOnce()

	committed, publishErr := f.publish(ctx, mod)
	published = committed
	if !committed {
		return publishErr
	}
	if publishErr != nil {
		// mod is live and reachable through the registry snapshot despite
		// publishErr — see Leader.publish's own doc comment for why
		// everything below still runs regardless.
		log.Error().Err(publishErr).Str("module", moduleName).
			Msg("hot reload (follower): module published but permission cache rebuild failed")
	}

	// This instance's own workflow-worker process needs the new binary
	// and a fresh credential too — the leader's own respawn on its
	// instance doesn't cover followers.
	if len(mod.Manifest.WorkflowTypes) > 0 {
		if err := f.Workers.Respawn(ctx, mod); err != nil {
			log.Error().Err(err).Str("module", moduleName).Msg("hot reload (follower): workflow-worker respawn failed")
		}
	}

	// Drain the old pool asynchronously — does not mutate oldMod.Status:
	// see Leader.Run's identical block for why.
	if oldMod != nil {
		go func() {
			oldMod.Pool.DrainAndClose(context.Background(), 30*time.Second)
			_ = oldMod.CompiledModule.Close(context.Background())
			log.Info().Str("module", moduleName).
				Str("old_version", oldMod.Manifest.Version).Str("new_version", version).
				Msg("hot reload (follower) complete")
		}()
	}

	// Never publishes engine:reload:{module} — that is the leader's own
	// job, run once per reload, not once per follower.
	return nil
}

// download reads key fully into memory from f.Storage — both the
// leader-published binary and its sibling manifest are small enough
// (manifest.Load itself caps a manifest at 1MB) that streaming isn't
// worth the complexity loader.Source's own []byte fields would need
// undone anyway.
func (f *Follower) download(ctx context.Context, key string) ([]byte, error) {
	rc, _, err := f.Storage.Download(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func (f *Follower) currentModules() map[string]*module.LoadedModule {
	snap := f.Registry.Snapshot()
	if snap == nil {
		return nil
	}
	return snap.Modules()
}

// reserve claims name for a follower run in progress via the registry's
// own shared Reserve — the same cross-writer-kind reservation
// moduleinstall.Worker and Leader already go through, so a follower
// attempt can't race a concurrent leader/install/follower run for the
// same module name on this instance. See errReloadInProgress's own doc
// comment (leader.go) for why this is purely advisory.
func (f *Follower) reserve(name string) (release func(), err error) {
	release, err = f.Registry.Reserve(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", errReloadInProgress, name)
	}
	return release, nil
}

// publish merges mod into the registry's current module map and rebuilds
// the permission cache that has to stay in lockstep with it — identical
// shape and identical locking rationale to Leader.publish (see its own
// doc comment): the two share the same *registry.ModuleRegistry lock, so
// a leader publishing one module and a follower publishing another can't
// have their RebuildAll calls interleave and silently overwrite one
// another's more current permission cache.
func (f *Follower) publish(ctx context.Context, mod *module.LoadedModule) (committed bool, err error) {
	f.Registry.Lock()
	defer f.Registry.Unlock()

	newSnap, err := f.Registry.UpdateWithLocked(func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
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

	if err := f.RolePerms.RebuildAll(ctx, f.TenantStore, f.RoleStore, newSnap.PermissionRegistry()); err != nil {
		return true, fmt.Errorf("rebuild role permission map: %w", err)
	}

	return true, nil
}
