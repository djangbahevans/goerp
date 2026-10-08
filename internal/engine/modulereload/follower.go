package modulereload

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/workflowworker"
	"github.com/djangbahevans/goerp/internal/engine/ws"
	"github.com/rs/zerolog/log"
)

// Follower adopts the leader's verified package in a separate registry and reuses schema
// sync completed against shared Postgres.
type Follower struct {
	Runtime     *wasm.Runtime
	PoolCfg     wasm.PoolConfig
	Registry    *registry.ModuleRegistry
	RolePerms   *permcache.RolePermissionMap
	TenantStore *tenant.Store
	RoleStore   *role.Store
	Storage     storage.Backend
	Workers     *workflowworker.Manager
	// Hub broadcasts schema.updated to this instance's own /_ws clients
	// once the reload is live locally. A follower has no syncResult (it
	// never runs tenant schema sync itself — the leader already did),
	// so unlike Leader.Run this reads TenantStore.ActiveTenants directly
	// rather than a per-run success list. Nil in tests that don't
	// exercise this — Run treats that the same as "nobody connected yet".
	Hub *ws.Hub
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

	release, err := reserveModule(f.Registry, moduleName)
	if err != nil {
		return err
	}
	releaseOnce := sync.OnceFunc(release)
	defer releaseOnce()

	wasmBytes, manifestBytes, err := f.downloadBoth(ctx, objectKey)
	if err != nil {
		return err
	}

	// Bundle storage keys use the module name and filename, independent of the
	// WASM/manifest object key.
	var bundleBytes []byte
	if mf, err := manifest.Load(manifestBytes); err == nil {
		if filename, err := module.BundleFilename(mf); err == nil && filename != "" {
			bundleBytes, err = f.download(ctx, module.BundleStorageKey(moduleName, filename))
			if err != nil {
				return fmt.Errorf("download published bundle: %w", err)
			}
		}
	}

	// Reverify downloaded checksums before loading. Followers reuse the leader's schema
	// sync because instances share Postgres.
	mod := loader.LoadModule(ctx, f.Runtime, f.PoolCfg, loader.Source{
		Name:          moduleName,
		ManifestBytes: manifestBytes,
		WasmBytes:     wasmBytes,
		BundleBytes:   bundleBytes,
	})
	if mod.Status == module.StatusFailed {
		return fmt.Errorf("load module: %s", mod.FailureReason)
	}

	// Close unpublished pools and compiled modules on failure; they are not reachable from
	// registry shutdown.
	published := false
	defer func() {
		if !published {
			mod.Pool.DrainAndClose(context.Background(), 5*time.Second)
			_ = mod.CompiledModule.Close(context.Background())
		}
	}()

	// A content-addressed key can hold a metadata republish with a different version.
	// Reject a delayed announcement before publishing the wrong version.
	if mod.Manifest.Version != version {
		return fmt.Errorf("downloaded manifest version %q does not match announced version %q for object %q", mod.Manifest.Version, version, objectKey)
	}

	oldMod := currentModules(f.Registry)[moduleName]
	mod.Status = module.StatusReady

	// The reservation's job is done; release it before publish acquires
	// its own narrower lock, matching Leader.Run's identical ordering.
	releaseOnce()

	committed, publishErr := publishModule(ctx, f.Registry, f.RolePerms, f.TenantStore, f.RoleStore, mod)
	published = committed
	if !committed {
		return publishErr
	}
	if publishErr != nil {
		// Publication errors can leave the module live; post-publication cleanup and
		// activation must still run.
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

	// Broadcast on this instance because WebSocket connections are process-local;
	// followers have no separate tenant sync outcomes.
	if f.Hub != nil {
		tenants, err := f.TenantStore.ActiveTenants(ctx)
		if err != nil {
			log.Warn().Err(err).Str("module", moduleName).
				Msg("hot reload (follower): failed to list active tenants for schema.updated broadcast")
		} else {
			payload := map[string]string{"module": moduleName}
			for _, t := range tenants {
				if _, err := f.Hub.Broadcast(ctx, ws.TenantChannel(t.ID), "schema.updated", payload); err != nil {
					log.Warn().Err(err).Str("module", moduleName).Str("tenant", t.Slug).
						Msg("hot reload (follower): broadcast schema.updated to tenant failed")
				}
			}
		}
	}

	// Never publishes engine:reload:{module} — that is the leader's own
	// job, run once per reload, not once per follower.
	return nil
}

// downloadBoth fetches the wasm binary and its sibling manifest
// concurrently — neither depends on the other's result, and both are only
// consumed together afterward at loader.LoadModule, so fetching them
// serially would pay a second full object-storage round trip for no
// reason, needlessly extending how long Run's own registry reservation
// stays held.
func (f *Follower) downloadBoth(ctx context.Context, objectKey string) (wasmBytes, manifestBytes []byte, err error) {
	var wg sync.WaitGroup
	var wasmErr, manifestErr error

	wg.Go(func() {
		wasmBytes, wasmErr = f.download(ctx, objectKey)
	})
	wg.Go(func() {
		manifestBytes, manifestErr = f.download(ctx, objectKey+".manifest.json")
	})
	wg.Wait()

	if wasmErr != nil {
		return nil, nil, fmt.Errorf("download published binary: %w", wasmErr)
	}
	if manifestErr != nil {
		return nil, nil, fmt.Errorf("download published manifest: %w", manifestErr)
	}
	return wasmBytes, manifestBytes, nil
}

// download reads one byte beyond the reported object size to detect false lengths while
// bounding memory across follower instances.
func (f *Follower) download(ctx context.Context, key string) ([]byte, error) {
	rc, size, err := f.Storage.Download(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(io.LimitReader(rc, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size {
		return nil, fmt.Errorf("read %d bytes, storage backend reported %d", len(data), size)
	}
	return data, nil
}
