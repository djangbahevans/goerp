// Package modulereload implements hotreload.LeaderFunc: the sequence a
// hot-reload leader instance runs end-to-end once it wins Redis
// leader-election for a (module, version) — validate, compile,
// downgrade-precheck and schema-sync every tenant, publish to object
// storage, publish the new registry snapshot, respawn the module's
// workflow-worker if it has one, drain the old pool, and announce
// (docs/engine-internals.md §10 "Hot reload sequence — leader path").
//
// Leader is a narrow, independently constructible struct rather than a
// method on *Engine, the same pattern internal/engine/moduleinstall.Worker
// and internal/engine/hotreload.Coordinator already use — it needs no
// Engine field hotreload.Coordinator's own tests don't already exercise.
package modulereload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantsync "github.com/djangbahevans/goerp/internal/engine/tenant/sync"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/workflowworker"
	"github.com/rs/zerolog/log"
)

// errReloadInProgress is reserve's fast-fail rejection when another reload
// of the same module is already running on this instance — mirrors
// moduleinstall's errInstallInProgress (goerp#489's reserve/mu split): it
// exists purely so a second local trigger for the same module (e.g. two
// different versions racing, or a retried Admin API call) doesn't waste a
// full compile/downgrade-check/sync run chasing an outcome the first run
// already owns.
var errReloadInProgress = errors.New("another reload of this module is already in progress")

// Leader implements hotreload.LeaderFunc via Run. Construct with the zero
// value plus its exported fields set (no constructor needed — matches
// moduleinstall.Worker).
type Leader struct {
	Runtime     *wasm.Runtime
	PoolCfg     wasm.PoolConfig
	Registry    *registry.ModuleRegistry
	RolePerms   *permcache.RolePermissionMap
	TenantStore *tenant.Store
	RoleStore   *role.Store
	SyncPool    *schema.SchemaSyncPool
	DiffEngine  *schema.SchemaDiffEngine
	Storage     storage.Backend
	Cache       *cache.Client
	Workers     *workflowworker.Manager
	// Concurrency bounds SyncModule's tenant fan-out; 0 uses
	// tenantsync.DefaultConcurrency.
	Concurrency int

	// mu guards only the final read-current-snapshot/merge/Update/
	// RebuildAll sequence in publish — not the compile, downgrade-check, or
	// tenant-sync phases that precede it. Same reasoning as
	// moduleinstall.Worker.mu (goerp#489): ModuleRegistry.Update replaces
	// its whole module map on every call, so two concurrent writers
	// building from the same starting snapshot could otherwise have the
	// second silently clobber the first's publish.
	mu sync.Mutex

	// reserveMu guards reloading, the set of module names with a reload
	// currently running on this instance. reserve claims a name before the
	// slow compile/downgrade-check/tenant-sync phases start, so a second
	// concurrent local trigger for the same module fails fast instead of
	// wasting that work — advisory only, released before publish runs (see
	// Run's own comment on why); publish's own mu-guarded recheck-by-
	// construction (it always installs whatever mod it's given, keyed by
	// name) is what actually decides the outcome.
	reserveMu sync.Mutex
	reloading map[string]struct{}
}

// Run implements hotreload.LeaderFunc. Coordinator already guarantees only
// one instance across the whole cluster ever runs Run for a given (module,
// version) at a time (its Redis SET NX lock) — reserve below covers the
// narrower, purely-local case Coordinator's own lock doesn't: two different
// trigger sources on this same instance racing for the same module (e.g.
// two different versions of it) at once.
func (l *Leader) Run(ctx context.Context, moduleName string, src loader.Source, m manifest.Manifest) error {
	// Object storage is warn-only at Engine startup (engine-internals.md
	// §2) — a connectivity failure there logs and continues rather than
	// failing New, so l.Storage (the same possibly-nil storage.Backend)
	// can reach here nil even on an otherwise healthy engine. Checked
	// before reserve/LoadModule, the same way workflowworker.spawn checks
	// its own storage dependency, so a reload with no object storage fails
	// fast with a clear error instead of a nil-pointer panic partway
	// through — and never wastes a compile/downgrade-check/sync run on
	// something object storage's own absence dooms regardless.
	if l.Storage == nil {
		return fmt.Errorf("object storage unavailable")
	}

	release, err := l.reserve(moduleName)
	if err != nil {
		return err
	}
	releaseOnce := sync.OnceFunc(release)
	defer releaseOnce()

	mod := loader.LoadModule(ctx, l.Runtime, l.PoolCfg, src)
	if mod.Status == module.StatusFailed {
		return fmt.Errorf("load module: %s", mod.FailureReason)
	}

	// From here on, mod owns a live pool and compiled module (LoadModule's
	// own internal defer only closes those for a failure inside LoadModule
	// itself, per its doc comment). published tracks whether mod made it
	// into the registry; if any step below fails first, mod is never
	// reachable through the registry, so nothing else will ever close it.
	published := false
	defer func() {
		if !published {
			mod.Pool.DrainAndClose(context.Background(), 5*time.Second)
			_ = mod.CompiledModule.Close(context.Background())
		}
	}()

	// Publish the validated binary + manifest to object storage under a
	// key derived from the manifest's own checksum — the same
	// checksum-as-key convention workflowworker.Manager.fetchAndVerify
	// already uses to fetch its own binary — so a follower instance
	// (goerp#490) can adopt the exact bytes this leader just validated,
	// regardless of how it separately noticed this reload (an Admin-API
	// upload only ever reaches the one instance the request hit; fsnotify
	// may never fire for an instance with a different module directory).
	objectKey := m.Checksum
	if _, err := l.Storage.Upload(ctx, objectKey, bytes.NewReader(src.WasmBytes), storage.UploadOptions{ContentType: "application/wasm"}); err != nil {
		return fmt.Errorf("publish binary to object storage: %w", err)
	}
	manifestBytes, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	manifestKey := objectKey + ".manifest.json"
	if _, err := l.Storage.Upload(ctx, manifestKey, bytes.NewReader(manifestBytes), storage.UploadOptions{ContentType: "application/json"}); err != nil {
		return fmt.Errorf("publish manifest to object storage: %w", err)
	}

	oldMod := l.currentModules()[moduleName]

	tenants, err := l.TenantStore.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("enumerate active tenants: %w", err)
	}

	// Downgrade pre-check: every active tenant, before any tenant's real
	// sync begins — a blocked downgrade for any tenant aborts the reload
	// before any DDL executes anywhere (engine-internals.md §10). Only
	// meaningful against a module that's already loaded; a module hot
	// reload discovers for the first time (no oldMod) can't be a downgrade
	// of anything.
	//
	// An infrastructure error opening one tenant's session (e.g. an
	// orphaned tenant row whose schema no longer exists) is logged and
	// skipped here rather than aborting every other tenant's reload — the
	// same partial-degradation principle SyncModule below already applies
	// to the real sync pass. Only an actual DowngradeStatusBlocked verdict
	// — a real, positive determination that this tenant's live schema
	// can't safely run the new code — is a hard abort, since that's the
	// specific correctness property this pre-check pass exists to
	// guarantee.
	if oldMod != nil {
		for _, t := range tenants {
			blocked, incompatibilities, err := l.checkTenantDowngrade(ctx, t, oldMod.Manifest.Version, mod)
			if err != nil {
				log.Warn().Err(err).Str("tenant", t.Slug).Str("module", moduleName).
					Msg("hot reload: downgrade pre-check failed for a tenant; proceeding without it")
				continue
			}
			if blocked {
				return fmt.Errorf("tenant %s: downgrade blocked: %v", t.Slug, incompatibilities)
			}
		}
	}

	// Per-tenant sync failures are logged by SyncModule itself and don't
	// abort the reload — same partial-degradation semantics
	// engine-internals.md's own Stage 4 documents ("a schema-sync failure
	// on one tenant doesn't block others") and moduleinstall.Worker.run
	// already applies to fresh installs.
	syncResult, err := tenantsync.SyncModule(ctx, l.SyncPool, l.DiffEngine, l.TenantStore, mod, l.Concurrency)
	if err != nil {
		return fmt.Errorf("sync tenants: %w", err)
	}
	if len(syncResult.Failed) > 0 {
		log.Warn().Str("module", moduleName).Int("failed_tenants", len(syncResult.Failed)).
			Msg("hot reload: schema sync failed for some tenants; reload proceeds for the rest")
	}
	mod.Status = module.StatusReady

	// The reservation's job — fast-failing a concurrent same-module reload
	// before it wastes compile/downgrade-check/sync work — is done; release
	// it before publish acquires its own narrower mu, so the two never
	// overlap (matches moduleinstall.Worker.run's identical ordering).
	releaseOnce()

	committed, err := l.publish(ctx, mod)
	published = committed // even a failed publish may have already committed mod to the registry (see publish's own doc comment) — never close a pool the registry now points to
	if err != nil {
		return err
	}

	// Respawn (not SpawnAll) is required here: SpawnAll's own spawn would
	// silently leak oldMod's still-running workflow-worker process by
	// overwriting its map entry without ever stopping it first — this is
	// exactly the "at module load (and hot reload), the engine downloads
	// workflow-worker... verifies it... and execs it" respawn
	// workflow-guide.md §3 describes, plus the "hot reload: a new version
	// gets a new credential, not a renewed old one" WorkflowWorkerCredential
	// rotation engine-internals.md §11 documents.
	if len(mod.Manifest.WorkflowTypes) > 0 {
		if err := l.Workers.Respawn(ctx, mod); err != nil {
			log.Error().Err(err).Str("module", moduleName).Msg("hot reload: workflow-worker respawn failed")
		}
	}

	// Drain the old pool asynchronously — does not mutate oldMod.Status:
	// oldMod is still reachable from whatever RegistrySnapshot a request
	// that started before publish's Store might be holding a reference to,
	// and nothing holding a reference to a published snapshot may ever
	// mutate it (registry.RegistrySnapshot's own doc comment).
	if oldMod != nil {
		go func() {
			oldMod.Pool.DrainAndClose(context.Background(), 30*time.Second)
			_ = oldMod.CompiledModule.Close(context.Background())
			log.Info().Str("module", moduleName).
				Str("old_version", oldMod.Manifest.Version).Str("new_version", m.Version).
				Msg("hot reload complete")
		}()
	}

	// Announce only on full success — every instance (including this one)
	// is subscribed; CurrentVersionAtLeast is what makes this leader's own
	// eventual receipt of its own announcement a no-op rather than a
	// special case (hotreload.Coordinator.OnReloadAnnouncement).
	if err := l.Cache.Publish(ctx, "engine:reload:"+moduleName, m.Version+":"+objectKey); err != nil {
		log.Error().Err(err).Str("module", moduleName).Msg("hot reload: failed to publish reload announcement")
	}

	return nil
}

// checkTenantDowngrade reports blocked=true only for a real
// DowngradeStatusBlocked verdict; any other error (opening the session,
// running the diff) comes back through err instead, for the caller to
// treat as a per-tenant infrastructure failure rather than a positive
// downgrade-blocked determination.
func (l *Leader) checkTenantDowngrade(ctx context.Context, t tenant.Tenant, currentVersion string, mod *module.LoadedModule) (blocked bool, incompatibilities []string, err error) {
	sess, err := l.SyncPool.BeginSync(ctx, t.ID, t.Slug, mod.Manifest.Name, &mod.Manifest)
	if err != nil {
		return false, nil, fmt.Errorf("begin downgrade pre-check session: %w", err)
	}
	defer func() {
		if closeErr := sess.Close(ctx); closeErr != nil {
			log.Warn().Err(closeErr).Str("tenant", t.Slug).Str("module", mod.Manifest.Name).
				Msg("could not close downgrade pre-check session")
		}
	}()

	status, incompatibilities, err := l.DiffEngine.CheckDowngrade(ctx, sess, currentVersion, mod.Manifest.Version, mod.ModelDecls, mod.TypeDecls)
	if err != nil {
		return false, nil, fmt.Errorf("downgrade pre-check: %w", err)
	}
	return status == schema.DowngradeStatusBlocked, incompatibilities, nil
}

func (l *Leader) currentModules() map[string]*module.LoadedModule {
	snap := l.Registry.Snapshot()
	if snap == nil {
		return nil
	}
	return snap.Modules()
}

// reserve claims name for a reload in progress on this instance, so a
// second concurrent local trigger for the same module fails immediately
// instead of running its own compile/downgrade-check/sync only to publish
// second. See errReloadInProgress's own doc comment for why this is purely
// advisory.
func (l *Leader) reserve(name string) (release func(), err error) {
	l.reserveMu.Lock()
	defer l.reserveMu.Unlock()

	if _, ok := l.reloading[name]; ok {
		return nil, fmt.Errorf("%w: %q", errReloadInProgress, name)
	}
	if l.reloading == nil {
		l.reloading = make(map[string]struct{})
	}
	l.reloading[name] = struct{}{}

	return func() {
		l.reserveMu.Lock()
		delete(l.reloading, name)
		l.reserveMu.Unlock()
	}, nil
}

// publish merges mod into the registry's current module map and rebuilds
// the permission cache that has to stay in lockstep with it — identical
// shape to moduleinstall.Worker.publish, just never rejecting an "already
// loaded" name the way that one does, since replacing an already-loaded
// module's entry is the entire point of a reload.
//
// committed reports whether Registry.Update itself succeeded, independent
// of err: a RebuildAll failure after a successful Update still returns
// committed=true, because mod is already live and reachable through the
// registry snapshot at that point — Run's own cleanup defer must not close
// mod's pool out from under a module the registry now routes traffic to,
// even though this call is still reporting an error (a stale permission
// cache, real but a lesser problem than closing a live module's pool).
func (l *Leader) publish(ctx context.Context, mod *module.LoadedModule) (committed bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	merged := maps.Clone(l.currentModules())
	if merged == nil {
		merged = make(map[string]*module.LoadedModule, 1)
	}
	merged[mod.Manifest.Name] = mod

	newSnap, err := l.Registry.Update(merged)
	if err != nil {
		return false, fmt.Errorf("publish module registry: %w", err)
	}

	if err := l.RolePerms.RebuildAll(ctx, l.TenantStore, l.RoleStore, newSnap.PermissionRegistry()); err != nil {
		return true, fmt.Errorf("rebuild role permission map: %w", err)
	}

	return true, nil
}
