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
	"encoding/json/v2"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/jobdispatch"
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
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// errReloadInProgress is reserve's fast-fail rejection when another writer
// — another reload of the same module, or an install of it — is already
// running, on this instance or (for an install) anywhere in the cluster.
// It exists purely so a second writer for the same module doesn't waste a
// full compile/downgrade-check/sync run chasing an outcome the first one
// already owns.
var errReloadInProgress = errors.New("another install or reload of this module is already in progress")

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
	// RiverClient inserts the data migration jobs jobdispatch.EnqueueApplicableDataMigration
	// builds, once mod is live in the registry (see Run's own comment on
	// why that ordering matters). Unlike moduleinstall.Worker's own
	// enqueue of the same helper, Run never runs as a River job itself
	// (its trigger sources are fsnotify/Redis pub-sub/Admin API/registry
	// poll, engine-internals.md §10) — river.ClientFromContext would have
	// nothing to find on ctx here, so this must be injected explicitly.
	RiverClient *river.Client[pgx.Tx]
	// Concurrency bounds SyncModule's tenant fan-out; 0 uses
	// tenantsync.DefaultConcurrency.
	Concurrency int
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

	release, err := reserveModule(l.Registry, moduleName)
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

	oldMod := currentModules(l.Registry)[moduleName]

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
		if err := l.checkAllTenantsDowngrade(ctx, tenants, oldMod.Manifest.Version, mod); err != nil {
			return err
		}
	}

	// Per-tenant sync failures are logged by SyncModule itself and don't
	// abort the reload — same partial-degradation semantics
	// engine-internals.md's own Stage 4 documents ("a schema-sync failure
	// on one tenant doesn't block others") and moduleinstall.Worker.run
	// already applies to fresh installs. SyncModuleTenants, not SyncModule:
	// tenants was already fetched above for the downgrade pre-check: no
	// reason to pay a second identical ActiveTenants round trip.
	syncResult := tenantsync.SyncModuleTenants(ctx, l.SyncPool, l.DiffEngine, tenants, mod, l.Concurrency)
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

	committed, publishErr := publishModule(ctx, l.Registry, l.RolePerms, l.TenantStore, l.RoleStore, mod)
	published = committed // even a failed publish may have already committed mod to the registry (see publish's own doc comment) — never close a pool the registry now points to
	if !committed {
		return publishErr
	}
	if publishErr != nil {
		// mod is live and reachable through the registry snapshot despite
		// publishErr (publish's own doc comment: committed=true here means
		// only RolePerms.RebuildAll failed, a stale permission cache — real
		// but a lesser problem than the alternative). Everything below this
		// point exists to finish what a live module needs regardless —
		// respawning its workflow-worker, draining the pool it just
		// replaced, and telling every other instance it can adopt this
		// version too — none of which becomes optional just because the
		// permission cache rebuild happened to fail. Returning early instead
		// would silently leak oldMod's pool forever (nothing else ever
		// closes it), leave the workflow-worker on its old binary/
		// credential, and leave every follower waiting on an announcement
		// that would never come — for a reload that, from every other
		// instance's perspective, already fully succeeded on this one.
		log.Error().Err(publishErr).Str("module", moduleName).
			Msg("hot reload: module published but permission cache rebuild failed")
	}

	// Trigger data migration dispatch (engine-internals.md §2 Stage 4 step
	// 26, migration-guide.md §4) only now that mod is live in the registry
	// under moduleName — jobdispatch.Worker resolves the target module
	// fresh from the registry snapshot at dispatch time, not from this
	// closure's mod, so enqueueing before publish risked a job racing
	// ahead of it and, on a publish failure, matching against whichever
	// module (old or none) is actually registered instead of this one.
	// Only tenants whose schema sync just succeeded are eligible — a
	// tenant schema sync failed for is not safely at mod.Manifest.Version
	// yet, so its own watermark evaluation is deferred to its next
	// successful sync. One handler enqueued per tenant here is enough:
	// jobdispatch.Worker enqueues the next applicable one itself once each
	// handler succeeds (EnqueueApplicableDataMigration's own doc comment).
	jobdispatch.EnqueueApplicableDataMigrations(ctx, l.RiverClient, l.SyncPool, syncResult.Succeeded, mod, "hot reload")

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

// checkAllTenantsDowngrade runs checkTenantDowngrade across tenants bounded
// to l.Concurrency concurrent checks (tenantsync.DefaultConcurrency if
// <= 0) — the same fan-out width the real sync pass right after this one
// uses, so this pre-check pass doesn't pay a fully serial round of
// session-open/diff/close calls immediately before a concurrent one for
// the identical tenant set. Returns on the first real DowngradeStatusBlocked
// verdict found (every tenant's check still gets to finish; only the first
// blocked one is reported, since one is already enough to abort the whole
// reload) — see checkTenantDowngrade's own doc comment for why a per-tenant
// infrastructure error is logged and skipped here rather than treated the
// same way.
func (l *Leader) checkAllTenantsDowngrade(ctx context.Context, tenants []tenant.Tenant, currentVersion string, mod *module.LoadedModule) error {
	concurrency := l.Concurrency
	if concurrency <= 0 {
		concurrency = tenantsync.DefaultConcurrency
	}
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var blockedErr error

	for _, t := range tenants {
		wg.Add(1)
		go func(t tenant.Tenant) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			blocked, incompatibilities, err := l.checkTenantDowngrade(ctx, t, currentVersion, mod)
			if err != nil {
				log.Warn().Err(err).Str("tenant", t.Slug).Str("module", mod.Manifest.Name).
					Msg("hot reload: downgrade pre-check failed for a tenant; proceeding without it")
				return
			}
			if blocked {
				mu.Lock()
				if blockedErr == nil {
					blockedErr = fmt.Errorf("tenant %s: downgrade blocked: %v", t.Slug, incompatibilities)
				}
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()

	return blockedErr
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
