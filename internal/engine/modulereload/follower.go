package modulereload

import (
	"context"
	"fmt"
	"io"
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

	// objectKey is content-addressed (Leader.Run's own objectKey :=
	// m.Checksum), not version-addressed: a metadata-only republish with
	// byte-identical wasm overwrites the manifest at this exact key in
	// place. A follower still processing an older, delayed announcement
	// for this same objectKey could otherwise silently adopt whatever
	// version is live at that key right now instead of the one it was
	// actually told to adopt — checked here, before publish, so a stale
	// announcement fails loudly instead of mis-registering a version.
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
		// mod is live and reachable through the registry snapshot despite
		// publishErr — see publishModule's own doc comment for why
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

// downloadBoth fetches the wasm binary and its sibling manifest
// concurrently — neither depends on the other's result, and both are only
// consumed together afterward at loader.LoadModule, so fetching them
// serially would pay a second full object-storage round trip for no
// reason, needlessly extending how long Run's own registry reservation
// stays held.
func (f *Follower) downloadBoth(ctx context.Context, objectKey string) (wasmBytes, manifestBytes []byte, err error) {
	var wg sync.WaitGroup
	var wasmErr, manifestErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		wasmBytes, wasmErr = f.download(ctx, objectKey)
	}()
	go func() {
		defer wg.Done()
		manifestBytes, manifestErr = f.download(ctx, objectKey+".manifest.json")
	}()
	wg.Wait()

	if wasmErr != nil {
		return nil, nil, fmt.Errorf("download published binary: %w", wasmErr)
	}
	if manifestErr != nil {
		return nil, nil, fmt.Errorf("download published manifest: %w", manifestErr)
	}
	return wasmBytes, manifestBytes, nil
}

// download reads key fully into memory from f.Storage, bounded by the
// size the backend itself already reports (e.g. Content-Length) rather
// than an unbounded io.ReadAll — hot reload fans this call out to every
// follower instance in the cluster at once off one announcement, so an
// unexpectedly large or corrupted object at key would otherwise get
// buffered fully in memory on every follower simultaneously. The
// LimitReader cap is size+1, not size: reading one byte past the
// backend's own reported length is how a stream that's actually longer
// than what the backend claimed gets caught below, instead of silently
// truncating it to a technically-valid-looking result.
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
