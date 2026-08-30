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

	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/moduleboot"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantsync "github.com/djangbahevans/goerp/internal/engine/tenant/sync"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/workflowworker"
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
	Workers     *workflowworker.Manager
	// Concurrency bounds SyncModule's tenant fan-out; 0 uses
	// tenantsync.DefaultConcurrency.
	Concurrency int
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
	// Every failure below except errAlreadyLoaded means a.PackagePath is
	// not — and never was — backing any successfully-loaded module, so
	// it's always safe to remove: leaving it behind would have Installer.
	// StartInstall's own doc comment broken by a later moduleboot.Discover
	// picking the same broken file back up on every future engine
	// restart, repeating the identical failure forever. errAlreadyLoaded
	// is excluded because that path is written under a deterministic
	// "{name}-{version}.erp" name — if the rejection is a second install
	// of the exact name/version that's already live, this file may well
	// be the one currently-loaded module's own backing file, which a
	// later Discover still needs to find. errInstallInProgress (reserve's
	// other rejection) is deliberately not excluded here: that name was
	// never actually loaded, so this file is always safe to remove.
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
	// Released explicitly right before publish below, once compile/sync
	// are done — not deferred to run's exit. releaseOnce makes that
	// explicit call and this defer safe together: whichever runs first
	// wins, and the other is a no-op. Deferring is still needed to cover
	// every early-return failure path between here and that explicit
	// call.
	releaseOnce := sync.OnceFunc(release)
	defer releaseOnce()

	m := loader.LoadModule(ctx, w.Runtime, w.PoolCfg, *src)
	if m.Status == module.StatusFailed {
		return Result{}, fmt.Errorf("load module: %s", m.FailureReason)
	}

	// From here on, m owns a live pool and compiled module (LoadModule's
	// own internal defer only closes those for a failure inside
	// LoadModule itself, per its doc comment — everything after this
	// point is this function's responsibility). published tracks whether
	// m made it into the registry; if any step below fails first, m is
	// never reachable through the registry, so nothing else will ever
	// close it.
	published := false
	defer func() {
		if !published {
			m.Pool.DrainAndClose(context.Background(), 5*time.Second)
			_ = m.CompiledModule.Close(context.Background())
		}
	}()

	// Snapshotted here, right before the check that uses it — not earlier,
	// alongside reserve above — to keep this as close to current as
	// possible: LoadModule's compile is the slow phase, and with mu no
	// longer held across it (goerp#487), another install can publish
	// during that window. existingModules can therefore still be one
	// publish stale by the time it's read (a residual, narrow race — not
	// fully eliminated, since fully eliminating it would mean re-locking
	// around this check and giving back the concurrency goerp#487 exists
	// for): a module rejected here because its subscription's owning
	// module published concurrently, just after this snapshot, would
	// succeed on a plain retry. Checked here, before any tenant is
	// touched, rather than as part of publish alongside the registry
	// merge, regardless: a module that fails this can't be allowed to run
	// schema sync at all — every other order lets real DDL land in every
	// active tenant for a module that's about to be rejected anyway, with
	// no way to undo it once applied (schema sync is additive-only by
	// design). Checking only m's own Subscribes against the existing
	// modules' Emits — rather than reusing loader.ValidateEventSubscriptions
	// across a merged map — also avoids mutating any *module.LoadedModule
	// already reachable from the live registry snapshot: existingModules
	// holds the exact pointers RegistrySnapshot.Modules() (documented
	// read-only) currently serves
	// to concurrent requests, and this check never writes to any of them,
	// only reads.
	existingModules := w.currentModules()
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

	// The reservation's job — fast-failing a concurrent same-name install
	// before it wastes compile/sync work — is done; release it before
	// publish acquires its own narrower mu, so the two never overlap.
	// publish's own recheck under mu is what's now authoritative: a
	// third install of this name reserved in the brief window between
	// this release and publish's lock would waste its own compile/sync
	// work, but would still correctly resolve to exactly one success and
	// one clean rejection once it reaches its own publish call.
	releaseOnce()

	committed, err := w.publish(ctx, m)
	published = committed // even a failed publish may have already committed m to the registry (see publish's doc comment) — never close a pool the registry now points to
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

	// Best-effort: the module is already live (published above) regardless
	// of whether its workflow-worker spawns cleanly, and retrying this job
	// on a workflow-worker failure would just hit the "already loaded"
	// guard above — so this is surfaced in Result rather than failing the
	// job.
	if err := w.Workers.SpawnAll(ctx, map[string]*module.LoadedModule{m.Manifest.Name: m}); err != nil {
		log.Error().Err(err).Str("module", m.Manifest.Name).Msg("module install: workflow-worker spawn failed")
		result.WorkflowWorkers = err.Error()
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
	knownEmits := make(map[string]bool)
	for _, other := range existingModules {
		if other.Status == module.StatusFailed {
			continue
		}
		for _, emit := range other.Manifest.Emits {
			knownEmits[emit.Name] = true
		}
	}
	for _, emit := range m.Manifest.Emits {
		knownEmits[emit.Name] = true
	}

	for _, sub := range m.Manifest.Subscribes {
		if knownEmits[sub.Name] {
			continue
		}
		owner, _, _ := strings.Cut(sub.Name, ".")
		if slices.Contains(m.Manifest.SoftDependsOn, owner) {
			log.Warn().Str("module", m.Manifest.Name).Str("event", sub.Name).
				Msg("subscribes to an event no loaded module emits; owning module is a soft dependency")
			continue
		}
		return fmt.Errorf("subscribes to unknown event %q", sub.Name)
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

// reserve claims name for an install in progress via the registry's own
// shared Reserve, so a second concurrent install of the same name — or a
// concurrent hot reload of it (modulereload.Leader shares this exact same
// call, against this exact same *registry.ModuleRegistry) — fails
// immediately instead of running its own compile/tenant-sync only to lose
// at publish time. Checks the live registry first (an already-loaded,
// non-failed module is a different rejection than "someone else is mid-
// write"); Reserve itself is advisory only — released before publish runs
// (see run's own comment on why), so publish's own UpdateWith-guarded
// recheck is what actually has to be correct; this exists purely so two
// writers racing for the same name don't both waste real compile/sync
// work chasing an outcome that's already decided.
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

// publish merges m into the registry's current module map and rebuilds
// the permission cache (permcache.RolePermissionMap) that has to stay in
// lockstep with it. It re-checks "already loaded" itself, inside
// UpdateWith's mutate closure — which runs with the registry's own
// writeMu held for both UpdateWithLocked and the RebuildAll call right
// after it, via Registry.Lock/Unlock rather than plain UpdateWith — a
// concurrent writer (another install, or a hot reload of a different
// module — modulereload.Leader.publish holds this exact same lock the same
// way) must not be able to publish and rebuild its own permission cache
// interleaved with this call's own pair, or whichever RebuildAll's
// (potentially slow, several-tenant) DB queries happen to finish last wins
// regardless of which one actually published last, silently reverting the
// live permission cache to a stale writer's view. UpdateWithLocked is
// handed the exact map about to be published, not a possibly-stale
// Snapshot() read — since run's own reservation-time check (see reserve)
// is released before this runs and so is only advisory by the time publish
// is reached. m's own event-subscription validity is checked by run before
// this is called (see run's own comment on why); ModuleRegistry.UpdateWithLocked
// below still separately validates route and job-type name conflicts
// against every other loaded module, which — unlike the subscription
// check — need Update's own full-map view to detect and can't be narrowed
// the same way.
//
// committed reports whether Registry.UpdateWithLocked itself succeeded,
// independent of err: a RebuildAll failure after a successful UpdateWithLocked
// still returns committed=true, because m is already live and reachable
// through the registry snapshot at that point — run's own cleanup defer
// must not close m's pool out from under a module the registry now
// routes traffic to, even though this call is still reporting an error
// (a stale permission cache, which is real but a lesser problem than
// closing a live module's pool).
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
