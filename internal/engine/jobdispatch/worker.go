// Package jobdispatch runs module job handlers through River and synchronous provider dispatch.
package jobdispatch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
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

// Worker cancels permanently invalid jobs and retries transient dispatch failures.
type Worker struct {
	river.WorkerDefaults[jobqueue.WASMJobArgs]
	ModuleRegistry *registry.ModuleRegistry
	SchemaSyncPool *schema.SchemaSyncPool
	Runtime        *wasm.Runtime
	TenantStore    *tenant.Store
	Roles          *role.Store
	Deliveries     DeliveryTracker
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

// Timeout is the declared timeout_seconds of the job type or cron job the job
// runs, or its documented default. River cancels the handler's context when it
// elapses, which closes the module instance; the attempt then fails and
// retries like any other error. Data-migration and provider jobs declare no
// timeout and keep River's client default.
func (w *Worker) Timeout(job *river.Job[jobqueue.WASMJobArgs]) time.Duration {
	args := job.Args
	if args.IsDataMigration || args.ProviderCategory != "" {
		return 0
	}
	snap := w.ModuleRegistry.Snapshot()
	if snap == nil {
		return 0
	}
	mod, ok := snap.Modules()[args.ModuleName]
	if !ok {
		return 0
	}
	if args.IsCron {
		for _, cron := range mod.Manifest.CronJobs {
			if cron.Name == args.JobType {
				return time.Duration(cron.EffectiveTimeoutSeconds()) * time.Second
			}
		}
		return 0
	}
	for _, jobType := range mod.Manifest.JobTypes {
		if jobType.Name == args.JobType {
			return time.Duration(jobType.EffectiveTimeoutSeconds()) * time.Second
		}
	}
	return 0
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

func (w *Worker) work(ctx context.Context, job *river.Job[jobqueue.WASMJobArgs], payload []byte) error {
	args := job.Args

	snap := w.ModuleRegistry.Snapshot()
	if snap == nil {
		return fmt.Errorf("module registry has no snapshot yet")
	}

	mod, err := readyModule(snap, args.ModuleName)
	if err != nil {
		return err
	}
	if mod.Pool == nil {
		return river.JobCancel(fmt.Errorf("module %q has no WASM instance pool (wasm: false)", args.ModuleName))
	}

	// Migration handlers and provider jobs are not declared in job_types.
	switch {
	case args.IsDataMigration:
		if !hasDataMigrationHandler(mod, args.JobType) {
			return river.JobCancel(fmt.Errorf("module %q has no declared data migration handler %q", args.ModuleName, args.JobType))
		}
	case args.IsCron:
		if !hasCronJob(mod, args.JobType) {
			return river.JobCancel(fmt.Errorf("module %q declares no cron job %q", args.ModuleName, args.JobType))
		}
	case args.ProviderCategory != "":
		// The provider was resolved at enqueue time; a module reloaded
		// since without this category in its provides must not receive
		// a job it no longer claims to handle.
		if !mod.Manifest.Provides[args.ProviderCategory] {
			return river.JobCancel(fmt.Errorf("module %q no longer provides %s", args.ModuleName, args.ProviderCategory))
		}
	default:
		if owner, ok := snap.JobRegistry().Owner(args.JobType); !ok || owner != args.ModuleName {
			return river.JobCancel(fmt.Errorf("job type %q is not registered to module %q", args.JobType, args.ModuleName))
		}
	}

	t, err := w.TenantStore.GetByID(ctx, args.TenantID)
	if err != nil {
		return fmt.Errorf("resolve tenant %s: %w", args.TenantID, err)
	}

	env := newJobEnvelope(job)
	env.Payload = payload
	if args.IsCron {
		env.Payload = nil
	}
	status, _, err := invokeHandleJob(ctx, w.Runtime, w.Roles, snap, mod, args, t.Slug, env, false)
	if err != nil {
		if errors.Is(err, role.ErrNotMember) || errors.Is(err, wasm.ErrNoHandleCron) {
			return river.JobCancel(err)
		}
		return err
	}
	export := "handle_job"
	if args.IsCron {
		export = "handle_cron"
	}
	if status == statusPermanent {
		return river.JobCancel(fmt.Errorf("%s for %s/%s returned a permanent failure", export, args.ModuleName, args.JobType))
	}
	if status != statusSuccess {
		return fmt.Errorf("%s for %s/%s returned status %d", export, args.ModuleName, args.JobType, status)
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

		riverClient := river.ClientFromContext[pgx.Tx](ctx)
		if err := EnqueueApplicableDataMigration(ctx, riverClient, w.SchemaSyncPool, args.TenantID, freshMod); err != nil {
			return fmt.Errorf("enqueue next data migration for %s/%s: %w", args.ModuleName, args.TenantID, err)
		}
	}

	return nil
}

func readyModule(snap *registry.RegistrySnapshot, moduleName string) (*module.LoadedModule, error) {
	mod, ok := snap.Modules()[moduleName]
	if !ok || mod.Status != module.StatusReady {
		return nil, fmt.Errorf("module %q is not ready", moduleName)
	}
	return mod, nil
}

// Session permissions are not captured: both queued and synchronous jobs resolve
// the acting user's current tenant identity before invoking the handler.
func invokeHandleJob(
	ctx context.Context,
	rt *wasm.Runtime,
	roles *role.Store,
	snap *registry.RegistrySnapshot,
	mod *module.LoadedModule,
	args jobqueue.WASMJobArgs,
	tenantSlug string,
	env abiv1.JobEnvelope,
	captureResult bool,
) (status int32, result []byte, err error) {
	if mod.Pool == nil {
		return 0, nil, fmt.Errorf("%w: module %q has no WASM instance pool (wasm: false)", wasm.ErrSyncJobTargetUnavailable, args.ModuleName)
	}

	// Migration jobs have engine-owned execution rules and no acting user.
	if args.IsDataMigration {
		args.UserID = ""
	}

	var actor role.ActingUser
	if args.UserID != "" {
		if roles == nil {
			return 0, nil, fmt.Errorf("resolve job user: identity resolver unavailable")
		}

		actor, err = roles.ResolveActingUser(ctx, tenantSlug, args.UserID)
		if err != nil {
			return 0, nil, fmt.Errorf("resolve job user %s: %w", args.UserID, err)
		}
	}

	env.UserID = args.UserID

	envelope, err := msgpack.Marshal(env)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal job envelope: %w", err)
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("borrow instance for %s: %w: %w", args.ModuleName, wasm.ErrSyncJobTargetUnavailable, err)
	}
	defer mod.Pool.Return(inst)

	moduleCtx := newModuleContext(rt, mod, args, actor, tenantSlug, snap)
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

	invoke, export := inst.InvokeHandleJob, "handle_job"
	if args.IsCron {
		invoke, export = inst.InvokeHandleCron, "handle_cron"
	}
	status, err = invoke(ctx, envelope)
	if err != nil {
		return 0, nil, fmt.Errorf("invoke %s for %s/%s: %w", export, args.ModuleName, args.JobType, err)
	}
	return status, moduleCtx.JobResult(), nil
}

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
func newModuleContext(rt *wasm.Runtime, mod *module.LoadedModule, args jobqueue.WASMJobArgs, actor role.ActingUser, tenantSlug string, snap *registry.RegistrySnapshot) *wasm.ModuleContext {
	mc := wasm.NewModuleContext("", mod.Manifest.Name, args.UserID, actor.ContactID, actor.Roles, nil, args.TenantID, tenantSlug, args.TraceID, mod.Capabilities, rt.TxLimiter(), wasm.ModuleSnapshot{
		ModelDecls:          mod.ModelDecls,
		FieldSecRegistry:    snap.FieldSecRegistry(),
		EventRegistry:       snap.EventRegistry(),
		DataAuditRegistry:   snap.DataAuditRegistry(),
		ComputedIndex:       snap.ComputedIndex(),
		ComputeTargets:      registry.ComputeTargets(snap),
		PermissionRegistry:  snap.PermissionRegistry(),
		PolicyRegistry:      snap.PolicyRegistry(),
		SearchIndexRegistry: snap.SearchIndexRegistry(),
		OwnedModels:         mod.Manifest.Schema.OwnedModels,
		ExtendsModels:       mod.Manifest.Schema.ExtendsModels,
		ConfigSchema:        mod.Manifest.ConfigSchema,
		UsesConfig:          mod.UsesConfig,
		JobTypes:            mod.Manifest.JobTypes,
		HTTPAllowlist:       mod.Manifest.HTTPAllowlist,
		ORMBulkMaxRows:      rt.ORMBulkMaxRows(),
		ORMStatementTimeout: rt.ORMStatementTimeout(),
	})

	if args.IsDataMigration {
		rt.SetDataMigrationContext(mc)
	}

	return mc
}

func hasCronJob(mod *module.LoadedModule, name string) bool {
	return slices.ContainsFunc(mod.Manifest.CronJobs, func(job manifest.CronJob) bool { return job.Name == name })
}

func hasDataMigrationHandler(mod *module.LoadedModule, handler string) bool {
	for _, dm := range mod.DataMigrations {
		if dm.Handler == handler {
			return true
		}
	}
	return false
}

// EnqueueApplicableDataMigration starts the next applicable migration for a tenant.
// Handlers run sequentially because later migrations can depend on earlier ones;
// Worker enqueues the next handler after advancing the version watermark.
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
		// Racing triggers must not rerun an identical migration, even after
		// cancellation or discard. The version watermark advances on success only.
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: jobqueue.UniqueAcrossAllJobStates},
	})
	if err != nil {
		return fmt.Errorf("enqueue data migration %q for tenant %s: %w", next.Handler, tenantID, err)
	}
	return nil
}

// EnqueueApplicableDataMigrations starts each tenant's migration chain after schema
// sync. Enqueue failures are logged independently so one tenant cannot block another.
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
