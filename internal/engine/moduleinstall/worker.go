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

	// mu serializes an entire install end to end — not just the registry
	// publish step. Two narrower problems both collapse into this one
	// fix: (1) ModuleRegistry.Update replaces its whole module map on
	// every call, so a read-current-snapshot/merge/Update sequence run by
	// two installs at once can have the second overwrite the first's
	// addition with a map built from a stale snapshot; (2) the "already
	// loaded" check below and the compile/sync/publish that follows it
	// are not atomic, so two concurrent installs of the same new module
	// name can both pass the check, both fully load and sync
	// independently, and whichever's own publish runs second silently
	// discards the other's module — which is also never closed (see
	// cleanup below), leaking its pool. The admin queue's default
	// concurrency (5) makes both scenarios real, not theoretical.
	// Install is not a hot path, so trading cross-install concurrency for
	// this being simply correct is the right tradeoff.
	mu sync.Mutex
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
	w.mu.Lock()
	defer w.mu.Unlock()

	// Every failure below except "already loaded" means a.PackagePath is
	// not — and never was — backing any successfully-loaded module, so
	// it's always safe to remove: leaving it behind would have Installer.
	// StartInstall's own doc comment broken by a later moduleboot.Discover
	// picking the same broken file back up on every future engine
	// restart, repeating the identical failure forever. "Already loaded"
	// is excluded because that path is written under a deterministic
	// "{name}-{version}.erp" name — if the rejection is a second install
	// of the exact name/version that's already live, this file may well
	// be the one currently-loaded module's own backing file, which a
	// later Discover still needs to find.
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

	existingModules := w.currentModules()
	if existing, ok := existingModules[src.Name]; ok && existing.Status != module.StatusFailed {
		return Result{}, fmt.Errorf("%w: %q (version %s) — installing a new version requires the upgrade path, not yet available (goerp#467)", errAlreadyLoaded, src.Name, existing.Manifest.Version)
	}

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

	// Checked here, before any tenant is touched, rather than as part of
	// publish alongside the registry merge: a module that fails this
	// can't be allowed to run schema sync at all — every other order lets
	// real DDL land in every active tenant for a module that's about to
	// be rejected anyway, with no way to undo it once applied (schema
	// sync is additive-only by design). Checking only m's own
	// Subscribes against the existing modules' Emits — rather than
	// reusing loader.ValidateEventSubscriptions across a merged map — also
	// avoids mutating any *module.LoadedModule already reachable from the
	// live registry snapshot: existingModules holds the exact pointers
	// RegistrySnapshot.Modules() (documented read-only) currently serves
	// to concurrent requests, and this check never writes to any of them,
	// only reads.
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

// publish merges m into the registry's current module map and rebuilds
// the permission cache (permcache.RolePermissionMap) that has to stay in
// lockstep with it — engine.go's own startup sequence documents this as
// registry.Update's only other caller. Callers must hold w.mu. m's own
// event-subscription validity is checked by run before this is called
// (see run's own comment on why); ModuleRegistry.Update below still
// separately validates route and job-type name conflicts against every
// other loaded module, which — unlike the subscription check — need
// Update's own full-map view to detect and can't be narrowed the same
// way.
//
// committed reports whether Registry.Update itself succeeded,
// independent of err: a RebuildAll failure after a successful Update
// still returns committed=true, because m is already live and reachable
// through the registry snapshot at that point — run's own cleanup defer
// must not close m's pool out from under a module the registry now
// routes traffic to, even though this call is still reporting an error
// (a stale permission cache, which is real but a lesser problem than
// closing a live module's pool).
func (w *Worker) publish(ctx context.Context, m *module.LoadedModule) (committed bool, err error) {
	merged := maps.Clone(w.currentModules())
	if merged == nil {
		merged = make(map[string]*module.LoadedModule, 1)
	}
	merged[m.Manifest.Name] = m

	newSnap, err := w.Registry.Update(merged)
	if err != nil {
		return false, fmt.Errorf("publish module registry: %w", err)
	}

	if err := w.RolePerms.RebuildAll(ctx, w.TenantStore, w.RoleStore, newSnap.PermissionRegistry()); err != nil {
		return true, fmt.Errorf("rebuild role permission map: %w", err)
	}

	return true, nil
}
