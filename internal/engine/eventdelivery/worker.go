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

// Worker processes jobqueue.EventDeliveryArgs jobs: for every async
// subscriber currently registered for the event, enqueue one
// jobqueue.SubscriberDeliveryArgs fan-out job, then write the immutable
// event_log audit row. A sync subscriber is never fanned out here — sync
// dispatch happens inline at emit time, not through this worker.
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
// ON CONFLICT (id) DO NOTHING.
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
		if !sub.Async {
			continue
		}
		if _, err := riverClient.Insert(ctx, jobqueue.SubscriberDeliveryArgs{
			EventID:     args.EventID,
			EventName:   args.EventName,
			ModuleName:  sub.ModuleName,
			HandlerName: sub.HandlerName,
			Payload:     args.Payload,
			TenantID:    args.TenantID,
			TraceID:     args.TraceID,
		}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}); err != nil {
			return fmt.Errorf("enqueue subscriber delivery for %s.%s: %w", sub.ModuleName, sub.HandlerName, err)
		}
	}

	t, err := w.TenantStore.GetByID(ctx, args.TenantID)
	if err != nil {
		return fmt.Errorf("resolve tenant %s: %w", args.TenantID, err)
	}

	query := fmt.Sprintf(`
		INSERT INTO %s.event_log (id, event_name, event_version, emitter_module, payload, trace_id, user_id, emitted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (id) DO NOTHING
	`, tenantschema.Name(t.Slug))
	if _, err := w.Pool.ExecContext(ctx, query,
		args.EventID, args.EventName, args.EventVersion, args.EmitterModule, args.Payload,
		nullIfEmpty(args.TraceID), nullIfEmpty(args.UserID),
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
