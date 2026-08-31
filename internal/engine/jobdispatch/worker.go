// Package jobdispatch implements Worker, the River worker that processes
// jobqueue.WASMJobArgs jobs (manifest-spec.md §26, goerp#110) by invoking
// the target module's handle_job WASM export. It lives in its own
// package, separate from internal/engine/jobqueue itself, for the same
// import-cycle reason internal/engine/eventdelivery does (see that
// package's own doc comment): it needs internal/engine/registry (to
// resolve a *module.LoadedModule by name) and internal/engine/wasm, and
// internal/engine/wasm already imports internal/engine/jobqueue, so
// registry (which reaches wasm via internal/engine/module) can never be
// imported back into jobqueue without a cycle. It also needs
// internal/engine/schema, for the same reason: schema has no dependency on
// this package or anything upstream of it (only internal/engine/db and
// internal/engine/manifest), so there's no cycle importing it here either.
package jobdispatch

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

// Worker processes jobqueue.WASMJobArgs jobs: resolve the target module,
// borrow an instance from its InstancePool, call InvokeHandleJob, and
// translate the result onto River's own retry semantics — a non-zero
// status or a Go/trap-level error both return a non-nil error from Work,
// so River retries per the job's own MaxAttempts (set at insert time,
// jobqueue.WASMJobArgs.InsertOpts) and marks it discarded once exhausted,
// never silently drops it. Doesn't itself build any richer per-job-type
// backoff/snooze policy — that's event-subscriber delivery's own
// retry_policy work (goerp#129), generalized to ordinary jobs by
// implementation-backlog.md #165, not this package's scope.
//
// SchemaSyncPool is only consulted for an IsDataMigration job (goerp#114):
// advancing the tenant's data_migration_version watermark on success, and
// enqueueing the next applicable handler, if any (EnqueueApplicableDataMigration
// below) — the same chaining step whatever first triggered this tenant's
// migration run (hot reload, module install, tenant provisioning) also
// calls, so a whole tenant's applicable-migration sequence only ever needs
// one external trigger to complete end to end, one handler at a time.
type Worker struct {
	river.WorkerDefaults[jobqueue.WASMJobArgs]
	ModuleRegistry *registry.ModuleRegistry
	SchemaSyncPool *schema.SchemaSyncPool
}

func (w *Worker) Work(ctx context.Context, job *river.Job[jobqueue.WASMJobArgs]) error {
	args := job.Args

	snap := w.ModuleRegistry.Snapshot()
	if snap == nil {
		// Shouldn't happen once the engine has started successfully
		// (ModuleRegistry.Update runs during Stage 3, well before any
		// worker can process a job) — guarded anyway, matching
		// eventdelivery.Worker's own nil-snapshot check. Returning an
		// error lets River retry once the registry is populated.
		return fmt.Errorf("module registry has no snapshot yet")
	}

	mod, ok := snap.Modules()[args.ModuleName]
	if !ok || mod.Status != module.StatusReady {
		return fmt.Errorf("module %q is not ready", args.ModuleName)
	}

	// Ownership is checked against two different name spaces depending on
	// who is allowed to have enqueued this class of job — see
	// jobqueue.WASMJobArgs.IsDataMigration's own doc comment for why a
	// data migration handler can't be checked against JobRegistry the way
	// an ordinary job is.
	if args.IsDataMigration {
		if !hasDataMigrationHandler(mod, args.JobType) {
			return fmt.Errorf("module %q has no declared data migration handler %q", args.ModuleName, args.JobType)
		}
	} else if owner, ok := snap.JobRegistry().Owner(args.JobType); !ok || owner != args.ModuleName {
		// A job whose declared (ModuleName, JobType) pair no longer
		// matches a live manifest declaration — a stale job surviving a
		// module removal/rename, or simply a caller-constructed args
		// value that named the wrong module for a real job type. Neither
		// is retryable: the mismatch won't resolve itself on a later
		// attempt.
		return fmt.Errorf("job type %q is not registered to module %q", args.JobType, args.ModuleName)
	}

	if mod.Pool == nil {
		// A module manifest can legitimately declare wasm: false (e.g.
		// the "theme" type requires it, manifest/module_type.go) and
		// still reach StatusReady with no compiled WASM at all — job_types
		// on such a module would be a manifest inconsistency nothing
		// today rejects at load time, but this must not panic on
		// mod.Pool.Borrow if it ever happens; not retryable, the mismatch
		// won't resolve itself on a later attempt.
		return fmt.Errorf("module %q has no WASM instance pool (wasm: false)", args.ModuleName)
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return fmt.Errorf("borrow instance for %s: %w", args.ModuleName, err)
	}
	defer mod.Pool.Return(inst)

	status, err := inst.InvokeHandleJob(ctx, args.Payload)
	if err != nil {
		return fmt.Errorf("invoke handle_job for %s/%s: %w", args.ModuleName, args.JobType, err)
	}
	if status != 0 {
		return fmt.Errorf("handle_job for %s/%s returned status %d", args.ModuleName, args.JobType, status)
	}

	if args.IsDataMigration {
		if err := w.SchemaSyncPool.AdvanceDataMigrationVersion(ctx, args.TenantID, args.ModuleName, args.MigrationToVersion); err != nil {
			return fmt.Errorf("advance data migration watermark for %s/%s: %w", args.ModuleName, args.TenantID, err)
		}

		// Re-resolved from a fresh snapshot rather than reusing mod above:
		// InvokeHandleJob can run long enough for a concurrent hot reload
		// of this exact module to publish a newer version in the
		// meantime, and DataMigrationWatermark's eligibility check needs
		// mod.Manifest.Version as of now, not as of when Work started.
		freshSnap := w.ModuleRegistry.Snapshot()
		if freshSnap == nil {
			return fmt.Errorf("module registry has no snapshot yet")
		}
		freshMod, ok := freshSnap.Modules()[args.ModuleName]
		if !ok {
			return fmt.Errorf("module %q no longer loaded", args.ModuleName)
		}

		// river.ClientFromContext is safe here regardless of which trigger
		// (hot reload, module install, tenant provisioning) originally
		// started this tenant's migration chain: this Work method only
		// ever runs as a real River job, so ctx is always River-managed.
		riverClient := river.ClientFromContext[pgx.Tx](ctx)
		if err := EnqueueApplicableDataMigration(ctx, riverClient, w.SchemaSyncPool, args.TenantID, freshMod); err != nil {
			return fmt.Errorf("enqueue next data migration for %s/%s: %w", args.ModuleName, args.TenantID, err)
		}
	}

	return nil
}

func hasDataMigrationHandler(mod *module.LoadedModule, handler string) bool {
	for _, dm := range mod.DataMigrations {
		if dm.Handler == handler {
			return true
		}
	}
	return false
}

// EnqueueApplicableDataMigration enqueues one jobqueue.WASMJobArgs job for
// the first data migration handler still applicable to tenantID's own
// data_migration_version watermark against mod's declared DataMigrations —
// or does nothing if none apply. Handlers run strictly one at a time per
// tenant (migration-guide.md §4 "Execution order" — later handlers may
// depend on an earlier one having already run): this only ever enqueues
// the single next handler, never the whole applicable set at once. Worker.Work
// calls this again itself once a handler succeeds, so the caller that
// triggers the very first call (hot reload leader, module install worker,
// tenant provisioning) is the only one that ever needs to call it
// directly — the rest of a tenant's chain drives itself.
func EnqueueApplicableDataMigration(ctx context.Context, riverClient *river.Client[pgx.Tx], pool *schema.SchemaSyncPool, tenantID string, mod *module.LoadedModule) error {
	if len(mod.DataMigrations) == 0 {
		return nil
	}

	watermark, eligible, err := pool.DataMigrationWatermark(ctx, tenantID, mod.Manifest.Name, mod.Manifest.Version)
	if err != nil {
		return fmt.Errorf("read data migration watermark: %w", err)
	}
	if !eligible {
		// The tenant isn't actually synced to mod.Manifest.Version with a
		// clean schema_sync_status yet — enqueueing now could run a
		// handler against schema DDL sync hasn't actually applied for
		// this tenant. Whatever eventually completes that sync
		// successfully calls this same function again.
		return nil
	}

	applicable, err := schema.ApplicableDataMigrations(watermark, mod.Manifest.Version, mod.DataMigrations)
	if err != nil {
		return fmt.Errorf("evaluate applicable data migrations: %w", err)
	}
	if len(applicable) == 0 {
		return nil
	}

	next := applicable[0]
	toVersion, err := schema.MigrationBoundaryVersion(next.ToVersion)
	if err != nil {
		return fmt.Errorf("migration %q: %w", next.Handler, err)
	}

	// The wire payload engine.DispatchDataMigration decodes on the
	// module's own side — shared directly via import with
	// sdk/go/model.MigrationJobPayload (see that type's own doc comment
	// for why this can be a direct import rather than an independently
	// mirrored copy).
	payload, err := msgpack.Marshal(model.MigrationJobPayload{
		Handler:     next.Handler,
		TenantID:    tenantID,
		FromVersion: watermark,
		ToVersion:   toVersion.String(),
	})
	if err != nil {
		return fmt.Errorf("encode data migration payload: %w", err)
	}

	_, err = riverClient.Insert(ctx, jobqueue.WASMJobArgs{
		ModuleName:           mod.Manifest.Name,
		JobType:              next.Handler,
		Payload:              payload,
		TenantID:             tenantID,
		IsDataMigration:      true,
		MigrationFromVersion: watermark,
		MigrationToVersion:   toVersion.String(),
	}, &river.InsertOpts{
		Queue: jobqueue.QueueDefault,
		// ByState covers redelivery after this exact migration already
		// ran to completion or was discarded — a handler's version range
		// only ever matches once a tenant's watermark has passed it, so a
		// repeat call here (e.g. two triggers racing to start the same
		// tenant's chain) must never re-run it. Matches
		// eventdelivery.Worker's identical use of
		// jobqueue.UniqueAcrossAllJobStates for the same reason.
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: jobqueue.UniqueAcrossAllJobStates},
	})
	if err != nil {
		return fmt.Errorf("enqueue data migration %q for tenant %s: %w", next.Handler, tenantID, err)
	}
	return nil
}

// EnqueueApplicableDataMigrations calls EnqueueApplicableDataMigration for
// every tenant in tenants — the shared "kick off each tenant's migration
// chain once its sync succeeds" step hot reload, module install, tenant
// provisioning, and the admin-triggered schema sync/accept jobs all need
// once mod is live in the registry. riverClient nil (not yet wired — e.g.
// before Engine.Start, or a test calling a Worker's run directly) is a
// no-op, not a panic. A per-tenant enqueue failure is logged, tagged with
// source (e.g. "hot reload", "module install"), and never blocks another
// tenant's — matching every other per-tenant failure in this pipeline
// (engine-internals.md §2 Stage 4's own "a schema-sync failure on one
// tenant doesn't block others").
func EnqueueApplicableDataMigrations(ctx context.Context, riverClient *river.Client[pgx.Tx], pool *schema.SchemaSyncPool, tenants []tenant.Tenant, mod *module.LoadedModule, source string) {
	if riverClient == nil {
		return
	}
	for _, t := range tenants {
		if err := EnqueueApplicableDataMigration(ctx, riverClient, pool, t.ID, mod); err != nil {
			log.Error().Err(err).Str("module", mod.Manifest.Name).Str("tenant", t.Slug).
				Msgf("%s: failed to enqueue data migration", source)
		}
	}
}
