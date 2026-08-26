// Package eventdelivery implements EventDeliveryWorker, the River worker
// that processes jobqueue.EventDeliveryArgs jobs (engine-internals.md §9
// "Event delivery worker"). It lives in its own package, separate from
// internal/engine/jobqueue itself, because it needs
// internal/engine/registry (to read the live EventRegistry) — and
// internal/engine/wasm already imports internal/engine/jobqueue
// (event_insert.go, for EventDeliveryArgs), so registry (which reaches
// wasm via internal/engine/module) can never be imported back into
// jobqueue without a cycle. internal/engine/tenantoffboard already
// established this same shape: a standalone package sitting above both
// jobqueue and registry, imported only by internal/engine/engine.go.
package eventdelivery

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Worker processes jobqueue.EventDeliveryArgs jobs: for every subscriber
// currently registered for the event that needs async delivery, enqueue
// one jobqueue.SubscriberDeliveryArgs fan-out job, then write the
// immutable event_log audit row. An async:true subscriber is always
// fanned out. An async:false subscriber is fanned out too, UNLESS
// args.SyncDispatched is set — meaning this emission requested inline
// synchronous dispatch (events.WithSync()) and that dispatch already ran,
// so fanning it out here would invoke the same handler a second time.
// SyncDispatched being unset (a plain Emit, or the EmitTx case that
// rejects WithSync() outright) is the documented fallback for an
// async:false subscriber whose emission never actually dispatched it
// synchronously (event-system.md §8) — it still needs delivering, just
// asynchronously instead of inline.
//
// The fan-out insert and the event_log write are deliberately not
// wrapped in one shared transaction: the running job's own client
// (river.ClientFromContext) is pgx-backed, while tenant-schema writes in
// this codebase (role.Store/invite.Store's own convention) go through
// the plain database/sql primary pool — different transaction/driver
// types with no bridge between them in this codebase today. Instead,
// both halves are independently idempotent, so a retry after a partial
// failure is safe: the fan-out insert is UniqueOpts{ByArgs: true}-deduped
// per (EventID, ModuleName, HandlerName), and the event_log write is
// ON CONFLICT (id, emitted_at) DO NOTHING — event_log is partitioned by
// emitted_at (goerp#194), which requires the partition key in every
// unique constraint, so args.EmittedAt (captured once at emit time,
// never recomputed here) is what keeps the conflict target stable across
// a retry.
type Worker struct {
	river.WorkerDefaults[jobqueue.EventDeliveryArgs]
	ModuleRegistry *registry.ModuleRegistry
	TenantStore    *tenant.Store
	Pool           *sql.DB
}

func (w *Worker) Work(ctx context.Context, job *river.Job[jobqueue.EventDeliveryArgs]) error {
	args := job.Args

	snap := w.ModuleRegistry.Snapshot()
	if snap == nil {
		// Shouldn't happen once the engine has started successfully
		// (ModuleRegistry.Update runs during Stage 3, well before any
		// worker can process a job) — guarded anyway, matching
		// tenantprovision.Activities.ListModuleNames' own defensive
		// nil-Snapshot check. Returning an error lets River retry once
		// the registry is populated, rather than silently under-
		// delivering to zero subscribers.
		return fmt.Errorf("module registry has no snapshot yet")
	}

	riverClient := river.ClientFromContext[pgx.Tx](ctx)
	for _, sub := range snap.EventRegistry().Subscribers(args.EventName) {
		if !sub.Async && args.SyncDispatched {
			continue
		}

		insertOpts := &river.InsertOpts{
			// ByState uses jobqueue.UniqueAcrossAllJobStates, not River's
			// "active"-only default, so a redelivery of the same event
			// (the same EventID/ModuleName/HandlerName) after the
			// original subscriber job already completed or was
			// discarded still dedupes against it instead of invoking the
			// handler's side effects a second time — the guarantee the
			// subscription-level idempotency_key_field: "event_id" case
			// (event-system.md §5) exists to provide.
			UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: jobqueue.UniqueAcrossAllJobStates},
		}
		if sub.RetryPolicy.MaxAttempts > 0 {
			insertOpts.MaxAttempts = sub.RetryPolicy.MaxAttempts
		}

		if _, err := riverClient.Insert(ctx, jobqueue.SubscriberDeliveryArgs{
			EventID:       args.EventID,
			EventName:     args.EventName,
			EventVersion:  args.EventVersion,
			EmitterModule: args.EmitterModule,
			ModuleName:    sub.ModuleName,
			HandlerName:   sub.HandlerName,
			Payload:       args.Payload,
			TenantID:      args.TenantID,
			UserID:        args.UserID,
			TraceID:       args.TraceID,
			EmittedAt:     args.EmittedAt,
		}, insertOpts); err != nil {
			return fmt.Errorf("enqueue subscriber delivery for %s.%s: %w", sub.ModuleName, sub.HandlerName, err)
		}
	}

	t, err := w.TenantStore.GetByID(ctx, args.TenantID)
	if err != nil {
		return fmt.Errorf("resolve tenant %s: %w", args.TenantID, err)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s.event_log (id, event_name, event_version, emitter_module, payload, trace_id, user_id, emitted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id, emitted_at) DO NOTHING
	`, tenantschema.Name(t.Slug))
	if _, err := w.Pool.ExecContext(ctx, query,
		args.EventID, args.EventName, args.EventVersion, args.EmitterModule, args.Payload,
		nullIfEmpty(args.TraceID), nullIfEmpty(args.UserID), args.EmittedAt,
	); err != nil {
		return fmt.Errorf("write event_log row: %w", err)
	}
	return nil
}

// nullIfEmpty maps an empty string to SQL NULL — trace_id/user_id are
// nullable columns, and a system-triggered emit with neither set
// shouldn't store as a literal empty string.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
