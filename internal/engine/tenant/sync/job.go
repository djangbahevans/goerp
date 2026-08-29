package tenantsync

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/riverqueue/river"
)

// SyncArgs is the River job goerp#292's `POST /admin/schema/sync` enqueues
// — an empty TenantSlug/ModuleName means "every active tenant"/"every
// loaded module", the same scoping `--tenant`/`--module` give
// cli-reference.md §4's `goerp schema sync`. Everything the request
// matches runs as one job (one job_id in the response, however many
// (tenant, module) pairs that expands to), fanned out inside Work rather
// than one job per pair.
type SyncArgs struct {
	TenantSlug string
	ModuleName string
}

func (SyncArgs) Kind() string { return "schema.sync" }

func (SyncArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobqueue.QueueAdmin}
}

// SyncPairResult is one (tenant, module) pair's outcome within a SyncResult.
type SyncPairResult struct {
	Tenant string `json:"tenant"`
	Module string `json:"module"`
	Error  string `json:"error,omitempty"`
}

// SyncResult is what SyncWorker.Work records via river.RecordOutput.
type SyncResult struct {
	Synced []SyncPairResult `json:"synced"`
	Failed []SyncPairResult `json:"failed"`
}

// SyncWorker runs SyncArgs by resolving which (tenant, module) pairs it
// scopes to, then calling SyncOne for each — the same per-pair logic
// SyncAll uses for Stage 4's own automatic sync, just reachable as an
// operator-triggered async job instead of only at engine startup. One
// pair failing doesn't stop the rest, matching SyncAll's own per-tenant
// failure isolation; the job itself only errors if enumerating tenants or
// resolving an explicitly-named tenant/module fails outright.
type SyncWorker struct {
	river.WorkerDefaults[SyncArgs]

	TenantStore *tenant.Store
	Registry    *registry.ModuleRegistry
	Pool        *schema.SchemaSyncPool
	DiffEngine  *schema.SchemaDiffEngine
}

func (w *SyncWorker) Work(ctx context.Context, job *river.Job[SyncArgs]) error {
	result, err := w.run(ctx, job.Args)
	if err != nil {
		return err
	}
	if err := river.RecordOutput(ctx, result); err != nil {
		return fmt.Errorf("record job output: %w", err)
	}
	return nil
}

// syncPair is one (tenant, module) unit of work within a SyncArgs job —
// run's own fanOut item type.
type syncPair struct {
	tenant tenant.Tenant
	mod    *module.LoadedModule
}

func (w *SyncWorker) run(ctx context.Context, a SyncArgs) (SyncResult, error) {
	tenants, err := w.resolveTenants(ctx, a.TenantSlug)
	if err != nil {
		return SyncResult{}, err
	}

	mods, err := w.resolveModules(a.ModuleName)
	if err != nil {
		return SyncResult{}, err
	}

	pairs := make([]syncPair, 0, len(tenants)*len(mods))
	for _, t := range tenants {
		for _, mod := range mods {
			pairs = append(pairs, syncPair{tenant: t, mod: mod})
		}
	}

	var result SyncResult
	var mu sync.Mutex
	fanOut(pairs, DefaultConcurrency, func(p syncPair) {
		pairResult := SyncPairResult{Tenant: p.tenant.Slug, Module: p.mod.Manifest.Name}
		if err := SyncOne(ctx, w.Pool, w.DiffEngine, p.tenant, p.mod, nil); err != nil {
			pairResult.Error = err.Error()
			mu.Lock()
			result.Failed = append(result.Failed, pairResult)
			mu.Unlock()
			return
		}
		mu.Lock()
		result.Synced = append(result.Synced, pairResult)
		mu.Unlock()
	})

	// Concurrent completion order is arbitrary — sort both slices by
	// (tenant, module) so a broad sync's result doesn't reshuffle between
	// otherwise-identical calls, matching Admin.Status's own pending-sweep
	// convention for the identical concurrency-vs-determinism tension.
	sortPairResults(result.Synced)
	sortPairResults(result.Failed)

	return result, nil
}

func sortPairResults(results []SyncPairResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Tenant != results[j].Tenant {
			return results[i].Tenant < results[j].Tenant
		}
		return results[i].Module < results[j].Module
	})
}

func (w *SyncWorker) resolveTenants(ctx context.Context, slug string) ([]tenant.Tenant, error) {
	if slug != "" {
		t, err := w.TenantStore.GetBySlug(ctx, slug)
		if err != nil {
			return nil, fmt.Errorf("look up tenant %q: %w", slug, err)
		}
		return []tenant.Tenant{*t}, nil
	}
	tenants, err := w.TenantStore.ActiveTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("enumerate active tenants: %w", err)
	}
	return tenants, nil
}

func (w *SyncWorker) resolveModules(name string) ([]*module.LoadedModule, error) {
	snap := w.Registry.Snapshot()
	if snap == nil {
		return nil, fmt.Errorf("module registry not ready")
	}

	if name != "" {
		mod, err := resolveModule(snap, name)
		if err != nil {
			return nil, err
		}
		return []*module.LoadedModule{mod}, nil
	}

	names := make([]string, 0, len(snap.Modules()))
	for n, mod := range snap.Modules() {
		if mod.Status == module.StatusFailed {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)

	mods := make([]*module.LoadedModule, len(names))
	for i, n := range names {
		mods[i] = snap.Modules()[n]
	}
	return mods, nil
}

// AcceptResyncArgs is the River job `POST /admin/schema/accept` enqueues
// after writing its acceptance rows — always scoped to exactly one
// (tenant, module) pair, unlike SyncArgs.
type AcceptResyncArgs struct {
	TenantSlug string
	ModuleName string
}

func (AcceptResyncArgs) Kind() string { return "schema.accept_resync" }

func (AcceptResyncArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobqueue.QueueAdmin}
}

// AcceptResyncWorker re-syncs one (tenant, module) pair via SyncOne, with
// whatever hashes SchemaSyncPool.AcceptedHashes returns as its accepted
// map — the accept handler already wrote the acceptance
// row(s) this job's own hash lookup will find before enqueuing this job,
// so there's no need to thread the accepted set through Args itself.
type AcceptResyncWorker struct {
	river.WorkerDefaults[AcceptResyncArgs]

	TenantStore *tenant.Store
	Registry    *registry.ModuleRegistry
	Pool        *schema.SchemaSyncPool
	DiffEngine  *schema.SchemaDiffEngine
}

func (w *AcceptResyncWorker) Work(ctx context.Context, job *river.Job[AcceptResyncArgs]) error {
	a := job.Args

	t, mod, err := resolveTenantModule(ctx, w.TenantStore, w.Registry, a.TenantSlug, a.ModuleName)
	if err != nil {
		return err
	}

	// Keyed by mod.Manifest.Version — whatever's loaded right now, at
	// execution time. If the module was upgraded between Accept and this
	// job running, the acceptance rows recorded under the old version
	// simply won't match here, so nothing gets auto-applied against a
	// diff the accepting operator never actually reviewed.
	accepted, err := w.Pool.AcceptedHashes(ctx, t.ID, a.ModuleName, mod.Manifest.Version)
	if err != nil {
		return fmt.Errorf("load accepted schema diff hashes: %w", err)
	}

	return SyncOne(ctx, w.Pool, w.DiffEngine, t, mod, accepted)
}
