package tenantsync

import (
	"context"
	"fmt"
	"sort"

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
	for _, t := range tenants {
		for _, mod := range mods {
			if err := SyncOne(ctx, w.Pool, w.DiffEngine, t, mod); err != nil {
				result.Failed = append(result.Failed, SyncPairResult{Tenant: t.Slug, Module: mod.Manifest.Name, Error: err.Error()})
				continue
			}
			result.Synced = append(result.Synced, SyncPairResult{Tenant: t.Slug, Module: mod.Manifest.Name})
		}
	}
	return result, nil
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
		mod, ok := snap.Modules()[name]
		if !ok {
			return nil, fmt.Errorf("module %q not loaded", name)
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

// AcceptResyncWorker re-syncs one (tenant, module) pair via
// SyncOneAccepted, loading whatever hashes SchemaSyncPool.AcceptedHashes
// returns for it — the accept handler already wrote the acceptance
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

	t, err := w.TenantStore.GetBySlug(ctx, a.TenantSlug)
	if err != nil {
		return fmt.Errorf("look up tenant %q: %w", a.TenantSlug, err)
	}

	snap := w.Registry.Snapshot()
	if snap == nil {
		return fmt.Errorf("module registry not ready")
	}
	mod, ok := snap.Modules()[a.ModuleName]
	if !ok {
		return fmt.Errorf("module %q not loaded", a.ModuleName)
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

	return SyncOneAccepted(ctx, w.Pool, w.DiffEngine, *t, mod, accepted)
}
