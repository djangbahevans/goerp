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

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

// handle_job's reserved status codes (manifest-spec.md §26), the same
// ones handle_event returns. Any value other than these is treated as a
// retryable failure.
const (
	statusSuccess   = 0
	statusPermanent = 2
)

// Worker processes jobqueue.WASMJobArgs jobs: resolve the target module,
// borrow an instance from its InstancePool, call InvokeHandleJob with the
// job's abiv1.JobEnvelope, and translate the result onto River's own retry
// semantics — status 2 (permanent) cancels the job via river.JobCancel;
// any other non-zero status or a Go/trap-level error returns a plain
// error from Work, so River retries per the job's own MaxAttempts (set at
// insert time, jobqueue.WASMJobArgs.InsertOpts) and marks it discarded
// once exhausted, never silently drops it. Doesn't itself build any
// richer per-job-type backoff/snooze policy — that's event-subscriber
// delivery's own retry_policy work (goerp#129), generalized to ordinary
// jobs by implementation-backlog.md #165, not this package's scope.
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
	// Runtime and TenantStore build the wasm.ModuleContext each
	// handle_job invocation runs under, which every host.* call from
	// inside a job handler needs.
	Runtime     *wasm.Runtime
	TenantStore *tenant.Store
	// Deliveries tracks a notification delivery job — one whose
	// NotificationID is set — on its notification's deliveries.
	Deliveries DeliveryTracker
}

// DeliveryTracker records a notification's sms_send or push_send job on
// its notification_deliveries rows — satisfied by
// *notify.ProviderDeliveries. Begin returns the payload the handler runs
// with, or ok false when nothing is left to send; Finish records the
// attempt's result, a failed Begin included, and returns the error Work
// reports.
type DeliveryTracker interface {
	Begin(ctx context.Context, args jobqueue.WASMJobArgs) (payload []byte, ok bool, err error)
	Finish(ctx context.Context, job *river.Job[jobqueue.WASMJobArgs], workErr error) error
}

func (w *Worker) Work(ctx context.Context, job *river.Job[jobqueue.WASMJobArgs]) error {
	if job.Args.NotificationID == "" {
		return w.work(ctx, job, job.Args.Payload)
	}
	if w.Deliveries == nil {
		return fmt.Errorf("notification delivery job %s/%s: no delivery tracker", job.Args.ModuleName, job.Args.JobType)
	}
	payload, ok, err := w.Deliveries.Begin(ctx, job.Args)
	if err != nil {
		return w.Deliveries.Finish(ctx, job, err)
	}
	if !ok {
		return nil
	}
	return w.Deliveries.Finish(ctx, job, w.work(ctx, job, payload))
}

// work runs job's handler with payload in place of the job's own.
func (w *Worker) work(ctx context.Context, job *river.Job[jobqueue.WASMJobArgs], payload []byte) error {
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

	mod, err := readyModule(snap, args.ModuleName)
	if err != nil {
		return err
	}

	// Ownership is checked against three different name spaces depending on
	// who is allowed to have enqueued this class of job — see
	// jobqueue.WASMJobArgs's own doc comment for why neither a data
	// migration handler nor a provider-category job can be checked against
	// JobRegistry the way an ordinary job is.
	switch {
	case args.IsDataMigration:
		if !hasDataMigrationHandler(mod, args.JobType) {
			return fmt.Errorf("module %q has no declared data migration handler %q", args.ModuleName, args.JobType)
		}
	case args.ProviderCategory != "":
		// The provider was resolved at enqueue time; a module reloaded
		// since without this category in its provides must not receive
		// a job it no longer claims to handle.
		if !mod.Manifest.Provides[args.ProviderCategory] {
			return fmt.Errorf("module %q no longer provides %s", args.ModuleName, args.ProviderCategory)
		}
	default:
		if owner, ok := snap.JobRegistry().Owner(args.JobType); !ok || owner != args.ModuleName {
			// A job whose declared (ModuleName, JobType) pair no longer
			// matches a live manifest declaration — a stale job surviving
			// a module removal/rename, or simply a caller-constructed args
			// value that named the wrong module for a real job type.
			// Neither is retryable: the mismatch won't resolve itself on
			// a later attempt.
			return fmt.Errorf("job type %q is not registered to module %q", args.JobType, args.ModuleName)
		}
	}

	// ModuleContext needs the tenant slug too (wasm.applyTenantScope builds
	// the search path from it); args only carries the ID — same reason
	// adminapi/activitydispatch.go's own moduleCtx construction resolves it.
	t, err := w.TenantStore.GetByID(ctx, args.TenantID)
	if err != nil {
		return fmt.Errorf("resolve tenant %s: %w", args.TenantID, err)
	}

	env := newJobEnvelope(job)
	env.Payload = payload
	status, _, err := invokeHandleJob(ctx, w.Runtime, snap, mod, args, t.Slug, env, false)
	if err != nil {
		return err
	}
	if status == statusPermanent {
		return river.JobCancel(fmt.Errorf("handle_job for %s/%s returned a permanent failure", args.ModuleName, args.JobType))
	}
	if status != statusSuccess {
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

// readyModule returns moduleName's loaded module when it is ready to run a
// handle_job invocation.
func readyModule(snap *registry.RegistrySnapshot, moduleName string) (*module.LoadedModule, error) {
	mod, ok := snap.Modules()[moduleName]
	if !ok || mod.Status != module.StatusReady {
		return nil, fmt.Errorf("module %q is not ready", moduleName)
	}
	if mod.Pool == nil {
		// A module manifest can legitimately declare wasm: false (e.g.
		// the "theme" type requires it, manifest/module_type.go) and
		// still reach StatusReady with no compiled WASM at all — job_types
		// on such a module would be a manifest inconsistency nothing
		// today rejects at load time, but this must not panic on
		// mod.Pool.Borrow if it ever happens; not retryable, the mismatch
		// won't resolve itself on a later attempt.
		return nil, fmt.Errorf("module %q has no WASM instance pool (wasm: false)", moduleName)
	}
	return mod, nil
}

// invokeHandleJob borrows an instance of mod and runs its handle_job
// export on env under a ModuleContext for args' tenant — the step
// Worker.Work and SyncDispatcher.DispatchJobSync share. With captureResult,
// the value the handler passes to host.jobs.set_result is returned as
// result; without it set_result is a no-op.
func invokeHandleJob(ctx context.Context, rt *wasm.Runtime, snap *registry.RegistrySnapshot, mod *module.LoadedModule, args jobqueue.WASMJobArgs, tenantSlug string, env abiv1.JobEnvelope, captureResult bool) (status int32, result []byte, err error) {
	envelope, err := msgpack.Marshal(env)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal job envelope: %w", err)
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("borrow instance for %s: %w: %w", args.ModuleName, wasm.ErrSyncJobTargetUnavailable, err)
	}
	defer mod.Pool.Return(inst)

	moduleCtx := newModuleContext(rt, mod, args, tenantSlug, snap)
	if captureResult {
		moduleCtx.CaptureJobResult()
	}
	inst.SetModuleContext(moduleCtx)
	rt.RegisterInstance(inst)
	defer func() {
		rt.UnregisterInstance(inst)
		moduleCtx.RollbackAll()
		inst.SetModuleContext(nil)
	}()

	status, err = inst.InvokeHandleJob(ctx, envelope)
	if err != nil {
		return 0, nil, fmt.Errorf("invoke handle_job for %s/%s: %w", args.ModuleName, args.JobType, err)
	}
	return status, moduleCtx.JobResult(), nil
}

// newJobEnvelope builds the abi.JobEnvelope handle_job receives for job.
func newJobEnvelope(job *river.Job[jobqueue.WASMJobArgs]) abiv1.JobEnvelope {
	args := job.Args
	return abiv1.JobEnvelope{
		JobID:           jobqueue.EncodeJobID(job.ID),
		JobType:         args.JobType,
		TenantID:        args.TenantID,
		ModuleName:      args.ModuleName,
		TraceID:         args.TraceID,
		Attempt:         job.Attempt,
		MaxAttempts:     job.MaxAttempts,
		IsDataMigration: args.IsDataMigration,
		Payload:         args.Payload,
	}
}

// Data-migration context is restricted to dispatched migration jobs so the
// migration_ddl capability alone cannot authorize DDL from an ordinary job.
func newModuleContext(rt *wasm.Runtime, mod *module.LoadedModule, args jobqueue.WASMJobArgs, tenantSlug string, snap *registry.RegistrySnapshot) *wasm.ModuleContext {
	mc := wasm.NewModuleContext("", mod.Manifest.Name, "", "", nil, nil, args.TenantID, tenantSlug, args.TraceID, mod.Capabilities, rt.TxLimiter(), wasm.ModuleSnapshot{
		ModelDecls:          mod.ModelDecls,
		FieldSecRegistry:    snap.FieldSecRegistry(),
		EventRegistry:       snap.EventRegistry(),
		ComputedIndex:       snap.ComputedIndex(),
		ComputeTargets:      registry.ComputeTargets(snap),
		PermissionRegistry:  snap.PermissionRegistry(),
		SearchIndexRegistry: snap.SearchIndexRegistry(),
		OwnedModels:         mod.Manifest.Schema.OwnedModels,
		ExtendsModels:       mod.Manifest.Schema.ExtendsModels,
		ConfigSchema:        mod.Manifest.ConfigSchema,
		JobTypes:            mod.Manifest.JobTypes,
		HTTPAllowlist:       mod.Manifest.HTTPAllowlist,
		ORMBulkMaxRows:      rt.ORMBulkMaxRows(),
		ORMStatementTimeout: rt.ORMStatementTimeout(),
	})
	mc.IsDataMigrationJob = args.IsDataMigration

	return mc
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

	// The JobEnvelope payload engine.DispatchJob decodes for a data
	// migration job on the module's own side.
	payload, err := msgpack.Marshal(abiv1.MigrationJobPayload{
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
		// ran to completion, was discarded, or was cancelled by a
		// handler's jobs.PermanentError — a handler's version range only
		// ever matches once a tenant's watermark has passed it, so a
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
