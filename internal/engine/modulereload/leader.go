// Package modulereload publishes leader reloads and lets followers adopt verified
// packages, coordinating registry swaps, worker replacement and pool draining.
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
	"github.com/djangbahevans/goerp/internal/engine/ws"
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
	// Hub broadcasts schema.updated to already-connected /_ws clients once
	// this reload is live, the same live-session convenience
	// moduleinstall.Worker.Hub already provides for module.installed. Nil
	// in tests that don't exercise this — Run treats that the same as
	// "nobody connected yet".
	Hub *ws.Hub

	// translationsMu orders frontend translation activations, which run
	// after a reload's reservation is released (activateTranslations).
	translationsMu sync.Mutex
}

// Run serializes local reloads for one module. The coordinator's distributed lock covers
// only one module/version pair, allowing different versions to contend locally.
func (l *Leader) Run(ctx context.Context, moduleName string, src loader.Source, m manifest.Manifest) error {
	// Storage is optional at engine startup but required for reload; reject its absence
	// before compilation or tenant DDL.
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

	// Close unpublished pools and compiled modules on failure; they are not reachable from
	// registry shutdown.
	published := false
	defer func() {
		if !published {
			mod.Pool.DrainAndClose(context.Background(), 5*time.Second)
			_ = mod.CompiledModule.Close(context.Background())
		}
	}()

	// Checksum-based storage keys let followers retrieve exactly the binary and manifest
	// validated by the leader.
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

	// Bundle keys use the module name and filename so older frontend URLs remain servable
	// after reloads.
	if err := module.PublishBundle(ctx, l.Storage, moduleName, &m, src.BundleBytes); err != nil {
		return fmt.Errorf("publish frontend bundle to object storage: %w", err)
	}
	// Translation uploads become visible only after module publication succeeds.
	translationsDigest, err := module.UploadFrontendTranslations(ctx, l.Storage, moduleName, m.Version, src.FrontendTranslations)
	if err != nil {
		return fmt.Errorf("upload frontend translations to object storage: %w", err)
	}

	oldMod := currentModules(l.Registry)[moduleName]

	tenants, err := l.TenantStore.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("enumerate active tenants: %w", err)
	}

	// Check all tenant downgrades before applying DDL. A blocked verdict aborts reload;
	// infrastructure failures warn and skip that tenant.
	if oldMod != nil {
		if err := l.checkAllTenantsDowngrade(ctx, tenants, oldMod.Manifest.Version, mod); err != nil {
			return err
		}
	}

	// Tenant sync failures degrade individual tenants without aborting the reload. Reuse
	// the tenant list fetched for the downgrade check.
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
	published = committed // A failed publish can still leave the module in the registry; closing its pool would
	// break live requests.
	if !committed {
		return publishErr
	}
	if err := l.activateTranslations(ctx, mod, translationsDigest); err != nil {
		log.Error().Err(err).Str("module", moduleName).Msg("hot reload: module published but its frontend translations were not made live")
	}
	if publishErr != nil {
		// Finish worker replacement, old-pool draining and follower announcements after
		// publication even when permission rebuild fails; the module is already live.
		log.Error().Err(publishErr).Str("module", moduleName).
			Msg("hot reload: module published but permission cache rebuild failed")
	}

	// Enqueue migrations only after registry publication and only for successfully synced
	// tenants. Workers resolve the target from the live snapshot.
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

	// Drain asynchronously without mutating the old module: requests can still hold an
	// immutable snapshot referencing it.
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

	// Live-session convenience only, same as moduleinstall.Worker's own
	// module.installed broadcast: a client with no open /_ws connection
	// sees the reloaded schema on its next GET /_meta/schema regardless.
	// Scoped to syncResult.Succeeded — a tenant whose schema sync just
	// failed isn't at mod.Manifest.Version yet, so its clients would be
	// told to refetch a schema their own tenant hasn't actually adopted.
	if l.Hub != nil {
		payload := map[string]string{"module": moduleName}
		for _, t := range syncResult.Succeeded {
			if _, err := l.Hub.Broadcast(ctx, ws.TenantChannel(t.ID), "schema.updated", payload); err != nil {
				log.Warn().Err(err).Str("module", moduleName).Str("tenant", t.Slug).
					Msg("hot reload: broadcast schema.updated to tenant failed")
			}
		}
	}

	return nil
}

// Downgrade checks use bounded concurrency and finish outstanding checks before returning
// the first blocked verdict. Infrastructure failures warn without becoming blocked
// verdicts.
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
		wg.Go(func() {
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
		})
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

// activateTranslations makes mod's frontend translation set live, unless a
// later reload of the same module has already replaced mod in the
// registry. The reservation that serializes same-module reloads is
// released before publish, so without this a slow publish could land its
// strings on top of a newer reload's. Under translationsMu, whichever
// reload is current when it checks activates, and a reload committing
// after that check queues behind it and activates last.
func (l *Leader) activateTranslations(ctx context.Context, mod *module.LoadedModule, digest string) error {
	l.translationsMu.Lock()
	defer l.translationsMu.Unlock()
	if currentModules(l.Registry)[mod.Manifest.Name] != mod {
		return nil
	}
	return module.ActivateFrontendTranslations(ctx, l.Storage, mod.Manifest.Name, mod.Manifest.Version, digest)
}
