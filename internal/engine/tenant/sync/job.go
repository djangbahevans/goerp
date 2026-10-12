package tenantsync

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobdispatch"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// SyncArgs scopes one job to a tenant/module pair or all active tenants/loaded modules
// when the corresponding selector is empty. The worker fans out matching pairs internally.
type SyncArgs struct {
	TenantSlug string
	ModuleName string
	RequestID  string
	TraceID    string
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

// SyncWorker isolates per-pair failures while syncing modules in dependency order.
type SyncWorker struct {
	river.WorkerDefaults[SyncArgs]

	TenantStore *tenant.Store
	Registry    *registry.ModuleRegistry
	Pool        *schema.SchemaSyncPool
	DiffEngine  *schema.SchemaDiffEngine
	// RiverClient triggers data migration dispatch after a pair syncs
	// successfully — nil-guarded (job_test.go calls run directly with a
	// plain context, the same reason moduleinstall.Worker's identical
	// field is), not river.ClientFromContext.
	RiverClient *river.Client[pgx.Tx]
	Runtime     *wasm.Runtime
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

func (w *SyncWorker) run(ctx context.Context, a SyncArgs) (SyncResult, error) {
	tenants, err := w.resolveTenants(ctx, a.TenantSlug)
	if err != nil {
		return SyncResult{}, err
	}

	mods, err := w.resolveModules(a.ModuleName)
	if err != nil {
		return SyncResult{}, err
	}

	var result SyncResult
	var mu sync.Mutex
	for _, mod := range module.OrderByDependencies(mods) {
		fanOut(tenants, DefaultConcurrency, func(t tenant.Tenant) {
			pairResult := SyncPairResult{Tenant: t.Slug, Module: mod.Manifest.Name}
			transition, err := SyncOneTransition(ctx, w.Pool, w.DiffEngine, t, mod, nil)
			if err != nil {
				pairResult.Error = err.Error()
				mu.Lock()
				result.Failed = append(result.Failed, pairResult)
				mu.Unlock()
				return
			}
			if w.Runtime != nil {
				w.Registry.Snapshot().NotifyModuleChange(ctx, w.Runtime, abiv1.Request{
					TenantID:   t.ID,
					TenantSlug: t.Slug,
					ID:         a.RequestID,
					TraceID:    a.TraceID,
				}, mod.Manifest.Name, transition.OldVersion, transition.Version)
			}

			jobdispatch.EnqueueApplicableDataMigrations(ctx, w.RiverClient, w.Pool, []tenant.Tenant{t}, mod, "schema sync")
			mu.Lock()
			result.Synced = append(result.Synced, pairResult)
			mu.Unlock()
		})
	}

	// Stable ordering keeps concurrent completion from reshuffling job output.
	sortPairResults(result.Synced)
	sortPairResults(result.Failed)

	return result, nil
}

func sortPairResults(results []SyncPairResult) {
	slices.SortFunc(results, func(a, b SyncPairResult) int {
		return cmp.Or(cmp.Compare(a.Tenant, b.Tenant), cmp.Compare(a.Module, b.Module))
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
	slices.Sort(names)

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
	RequestID  string
	TraceID    string
}

func (AcceptResyncArgs) Kind() string { return "schema.accept_resync" }

func (AcceptResyncArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobqueue.QueueAdmin}
}

// AcceptResyncWorker reads persisted acceptance hashes at execution time rather than
// carrying consent in job arguments.
type AcceptResyncWorker struct {
	river.WorkerDefaults[AcceptResyncArgs]

	TenantStore *tenant.Store
	Registry    *registry.ModuleRegistry
	Pool        *schema.SchemaSyncPool
	DiffEngine  *schema.SchemaDiffEngine
	// RiverClient triggers data migration dispatch after the resync
	// succeeds — nil-guarded for the same reason SyncWorker's own field is.
	RiverClient *river.Client[pgx.Tx]
	Runtime     *wasm.Runtime
}

func (w *AcceptResyncWorker) Work(ctx context.Context, job *river.Job[AcceptResyncArgs]) error {
	a := job.Args

	t, mod, err := resolveTenantModule(ctx, w.TenantStore, w.Registry, a.TenantSlug, a.ModuleName)
	if err != nil {
		return err
	}

	// Match the loaded version at execution time so an upgrade after acceptance cannot
	// apply changes the operator did not review.
	accepted, err := w.Pool.AcceptedHashes(ctx, t.ID, a.ModuleName, mod.Manifest.Version)
	if err != nil {
		return fmt.Errorf("load accepted schema diff hashes: %w", err)
	}

	transition, err := SyncOneTransition(ctx, w.Pool, w.DiffEngine, t, mod, accepted)
	if err != nil {
		return err
	}

	if w.Runtime != nil {
		w.Registry.Snapshot().NotifyModuleChange(ctx, w.Runtime, abiv1.Request{
			TenantID:   t.ID,
			TenantSlug: t.Slug,
			ID:         a.RequestID,
			TraceID:    a.TraceID,
		}, mod.Manifest.Name, transition.OldVersion, transition.Version)
	}

	jobdispatch.EnqueueApplicableDataMigrations(ctx, w.RiverClient, w.Pool, []tenant.Tenant{t}, mod, "accept resync")
	return nil
}
