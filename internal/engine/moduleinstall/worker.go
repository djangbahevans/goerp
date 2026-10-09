package moduleinstall

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobdispatch"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/moduleboot"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
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

// errAlreadyLoaded distinguishes the "already loaded" rejection from
// every other run failure — run's cleanup only removes the persisted
// package file for the latter (see run's own comment on why).
var errAlreadyLoaded = errors.New("module already loaded")

// errInstallInProgress is reserve's fast-fail rejection when another
// install of the same name is already running — unlike errAlreadyLoaded,
// this name was never actually loaded, so the loser's package file is not
// anyone's backing file and run's cleanup must still remove it.
var errInstallInProgress = errors.New("another install of this module is already in progress")

// alreadyLoadedErr builds the errAlreadyLoaded rejection reserve and
// publish both return for the identical condition (a live, non-failed
// module already occupying name), so the message can't drift between the
// two call sites.
func alreadyLoadedErr(name string, existing *module.LoadedModule) error {
	return fmt.Errorf("%w: %q (version %s) — installing a new version requires the upgrade path, not yet available (goerp#467)", errAlreadyLoaded, name, existing.Manifest.Version)
}

// Worker runs Args: loads the persisted package via loader.LoadModule
// (the same compile/capabilities/pool/get_routes/get_model_declarations/
// EnableViews path engine startup uses for every other module), syncs it
// across every active tenant, and — once every tenant's sync has finished
// one way or the other — publishes it into the live ModuleRegistry and
// rebuilds the permission cache that has to stay in lockstep with it.
type Worker struct {
	river.WorkerDefaults[Args]

	Runtime     *wasm.Runtime
	PoolCfg     wasm.PoolConfig
	Registry    *registry.ModuleRegistry
	RolePerms   *permcache.RolePermissionMap
	TenantStore *tenant.Store
	RoleStore   *role.Store
	SyncPool    *schema.SchemaSyncPool
	DiffEngine  *schema.SchemaDiffEngine
	// Storage publishes frontend bundles; nil storage warns and skips publication without
	// failing the install.
	Storage storage.Backend
	Workers *workflowworker.Manager
	// RiverClient is wired after queue construction and inserts migration jobs without
	// requiring a River worker context.
	RiverClient *river.Client[pgx.Tx]
	// Concurrency bounds SyncModule's tenant fan-out; 0 uses
	// tenantsync.DefaultConcurrency.
	Concurrency int
	// Hub broadcasts install completion to connected clients. Nil disables the broadcast.
	Hub *ws.Hub
}

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
	result, err := w.run(ctx, job.Args)
	if err != nil {
		return err
	}
	// A RecordOutput failure here is logged, not returned: run has
	// already fully succeeded and published the module by this point, so
	// failing Work would make River retry the job — and a retry can only
	// ever hit the "already loaded" guard at the top of run and be
	// permanently rejected, since the module install itself already
	// happened. Losing the recorded Result (what a polling `jobs show`
	// displays) is a strictly smaller problem than poisoning the job into
	// an unretriable failure for work that's already done.
	if err := river.RecordOutput(ctx, result); err != nil {
		log.Error().Err(err).Str("module", result.Module).Msg("module install: record job output failed (install itself succeeded)")
	}
	return nil
}

// run is Work's plain-Go core, callable without a real River execution
// context — same split every other worker in this codebase documents.
func (w *Worker) run(ctx context.Context, a Args) (result Result, err error) {
	// Remove failed install packages to prevent rediscovery on restart. Preserve
	// errAlreadyLoaded paths because their deterministic filename may back the live
	// module.
	defer func() {
		if err != nil && !errors.Is(err, errAlreadyLoaded) {
			if rmErr := os.Remove(a.PackagePath); rmErr != nil && !os.IsNotExist(rmErr) {
				log.Warn().Err(rmErr).Str("path", a.PackagePath).Msg("module install: could not remove persisted package after failed install")
			}
		}
	}()

	data, err := os.ReadFile(a.PackagePath)
	if err != nil {
		return Result{}, fmt.Errorf("read persisted package %q: %w", a.PackagePath, err)
	}
	src, _, err := moduleboot.ParsePackage(data)
	if err != nil {
		return Result{}, err
	}
	src.PackagePath = a.PackagePath

	release, err := w.reserve(src.Name)
	if err != nil {
		return Result{}, err
	}
	// Release the reservation before publishing, while deferred release covers every early
	// return.
	releaseOnce := sync.OnceFunc(release)
	defer releaseOnce()

	m := loader.LoadModule(ctx, w.Runtime, w.PoolCfg, *src)
	if m.Status == module.StatusFailed {
		return Result{}, fmt.Errorf("load module: %s", m.FailureReason)
	}

	// Bundle publication uses the shared module/file storage key; failure warns without
	// rejecting the installed module.
	if err := module.PublishBundle(ctx, w.Storage, m.Manifest.Name, &m.Manifest, src.BundleBytes); err != nil {
		if errors.Is(err, module.ErrNoStorageBackend) {
			log.Warn().Str("module", m.Manifest.Name).Msg("module install: frontend bundle declared but no object storage backend is configured; bundle will not be servable")
		} else {
			log.Warn().Err(err).Str("module", m.Manifest.Name).Msg("module install: publish frontend bundle to object storage failed")
		}
	}
	if err := module.PublishFrontendTranslations(ctx, w.Storage, m.Manifest.Name, m.Manifest.Version, src.FrontendTranslations); err != nil {
		if errors.Is(err, module.ErrNoStorageBackend) {
			log.Warn().Str("module", m.Manifest.Name).Msg("module install: frontend translations present but no object storage backend is configured; they will not be servable")
		} else {
			log.Warn().Err(err).Str("module", m.Manifest.Name).Msg("module install: publish frontend translations to object storage failed")
		}
	}

	// Close unpublished pools and compiled modules on failure; they are not reachable from
	// registry shutdown.
	published := false
	defer func() {
		if !published {
			m.Pool.DrainAndClose(context.Background(), 5*time.Second)
			_ = m.CompiledModule.Close(context.Background())
		}
	}()

	// Validate subscriptions before irreversible tenant DDL. Snapshot after compilation to
	// reduce staleness, and read existing modules without mutating live registry pointers;
	// a concurrently published dependency may require retrying.
	existingModules := w.currentModules()
	if err := loader.ValidateModelExtensionCandidate(m, existingModules); err != nil {
		return Result{}, fmt.Errorf("validate model extensions: %w", err)
	}
	if err := validateNewModuleSubscriptions(m, existingModules); err != nil {
		m.Fail(err.Error())
		return Result{}, fmt.Errorf("validate event subscriptions: %w", err)
	}

	if len(m.Manifest.NotificationTypes) > 0 {
		nt, err := notiftemplate.Load(m.Manifest.NotificationTypes, m.PackagePath)
		if err != nil {
			return Result{}, fmt.Errorf("load notification templates: %w", err)
		}
		m.NotifTemplates = nt
	}

	syncResult, err := tenantsync.SyncModule(ctx, w.SyncPool, w.DiffEngine, w.TenantStore, m, w.Concurrency)
	if err != nil {
		return Result{}, fmt.Errorf("sync tenants: %w", err)
	}
	m.Status = module.StatusReady

	// Release the reservation before acquiring the publication lock. Publication rechecks
	// the name under that lock, so a concurrent reservation cannot create duplicate
	// modules.
	releaseOnce()

	committed, err := w.publish(ctx, m)
	published = committed // A failed publish can still leave the module in the registry; closing its pool would
	// break live requests.
	if err != nil {
		return Result{}, err
	}

	result = Result{
		Module:    m.Manifest.Name,
		Version:   m.Manifest.Version,
		Succeeded: make([]string, 0, len(syncResult.Succeeded)),
		Failed:    make([]TenantResult, 0, len(syncResult.Failed)),
	}
	for _, t := range syncResult.Succeeded {
		result.Succeeded = append(result.Succeeded, t.Slug)
	}
	for _, r := range syncResult.Failed {
		result.Failed = append(result.Failed, TenantResult{Tenant: r.Tenant.Slug, Error: r.Err.Error()})
	}

	// Migration dispatch follows registry publication so workers can resolve the installed
	// module.
	jobdispatch.EnqueueApplicableDataMigrations(ctx, w.RiverClient, w.SyncPool, syncResult.Succeeded, m, "module install")

	// The module is already published. A worker spawn failure is reported in Result
	// because retrying the install would hit the already-loaded guard.
	if err := w.Workers.SpawnAll(ctx, map[string]*module.LoadedModule{m.Manifest.Name: m}); err != nil {
		log.Error().Err(err).Str("module", m.Manifest.Name).Msg("module install: workflow-worker spawn failed")
		result.WorkflowWorkers = err.Error()
	}

	// Broadcast last so stalled clients cannot consume the job's budget before migration
	// dispatch and worker startup.
	if w.Hub != nil {
		payload := map[string]string{"module": m.Manifest.Name}
		for _, t := range syncResult.Succeeded {
			if _, err := w.Hub.Broadcast(ctx, ws.TenantChannel(t.ID), "module.installed", payload); err != nil {
				log.Warn().Err(err).Str("module", m.Manifest.Name).Str("tenant", t.Slug).
					Msg("module install: broadcast to tenant failed")
			}
		}
	}

	return result, nil
}

// validateNewModuleSubscriptions checks only m's own Manifest.Subscribes
// against what existingModules' non-failed entries emit (plus m's own
// Manifest.Emits) — the same rule loader.ValidateEventSubscriptions
// applies, narrowed to never touch (or even need to consider revisiting)
// any *module.LoadedModule beyond m itself. Safe by construction: adding
// one new module can only ever grow the set of known emits, so it can
// never retroactively break an already-loaded module's own subscription
// that already passed this same check when that module was loaded.
func validateNewModuleSubscriptions(m *module.LoadedModule, existingModules map[string]*module.LoadedModule) error {
	emits := make(map[string][]manifest.EventDeclaration)
	for _, other := range existingModules {
		if other.Status == module.StatusFailed {
			continue
		}
		for _, emit := range other.Manifest.Emits {
			emits[emit.Name] = append(emits[emit.Name], emit)
		}
	}
	for _, emit := range m.Manifest.Emits {
		emits[emit.Name] = append(emits[emit.Name], emit)
	}

	for _, sub := range m.Manifest.Subscribes {
		problem := loader.EventSubscriptionProblem(sub, emits[sub.Name])
		if problem == "" {
			continue
		}
		owner, _, _ := strings.Cut(sub.Name, ".")
		if slices.Contains(m.Manifest.SoftDependsOn, owner) {
			log.Warn().Str("module", m.Manifest.Name).Str("event", sub.Name).Int("version", sub.EffectiveVersion()).
				Msg("subscribes to an event version no loaded module emits; owning module is a soft dependency")
			continue
		}
		return errors.New(problem)
	}
	return nil
}

func (w *Worker) currentModules() map[string]*module.LoadedModule {
	snap := w.Registry.Snapshot()
	if snap == nil {
		return nil
	}
	return snap.Modules()
}

// reserve shares the registry name gate with hot reload to avoid duplicate compile/sync
// work. The publication recheck under the registry lock remains authoritative.
func (w *Worker) reserve(name string) (release func(), err error) {
	if existing, ok := w.currentModules()[name]; ok && existing.Status != module.StatusFailed {
		return nil, alreadyLoadedErr(name, existing)
	}
	release, err = w.Registry.Reserve(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", errInstallInProgress, name)
	}
	return release, nil
}

// publish locks registry merge and permission rebuild together to prevent stale cache
// publication. committed remains true if only the rebuild fails, so cleanup preserves the
// live module's pool.
func (w *Worker) publish(ctx context.Context, m *module.LoadedModule) (committed bool, err error) {
	w.Registry.Lock()
	defer w.Registry.Unlock()

	newSnap, err := w.Registry.UpdateWithLocked(func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		if existing, ok := current[m.Manifest.Name]; ok && existing.Status != module.StatusFailed {
			return nil, alreadyLoadedErr(m.Manifest.Name, existing)
		}

		merged := maps.Clone(current)
		if merged == nil {
			merged = make(map[string]*module.LoadedModule, 1)
		}
		// A module installed after boot depends only on modules already
		// loaded (nothing yet could depend on it), so appending it after
		// the current highest LoadOrder keeps load_order dependencies-first
		// without re-running moduleboot.Order across the whole registry.
		m.LoadOrder = len(merged)
		merged[m.Manifest.Name] = m
		return merged, nil
	})
	if err != nil {
		return false, fmt.Errorf("publish module registry: %w", err)
	}

	if err := w.RolePerms.RebuildAll(ctx, w.TenantStore, w.RoleStore, newSnap.PermissionRegistry()); err != nil {
		return true, fmt.Errorf("rebuild role permission map: %w", err)
	}

	return true, nil
}
