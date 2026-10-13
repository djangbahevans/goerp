// Package eventdelivery fans out and invokes event subscribers through River jobs. It sits
// above queue and registry packages to avoid their dependency cycle through WASM.
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

// Worker fans out event deliveries and writes the event log. Fan-out uniqueness and a
// stable (id, emitted_at) conflict key make retries safe across the separate queue and
// audit transactions.
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
		return fmt.Errorf("module registry has no snapshot yet")
	}

	riverClient := river.ClientFromContext[pgx.Tx](ctx)
	for _, sub := range snap.EventRegistry().Subscribers(args.EventName, args.EventVersion) {
		if !sub.Async && args.SyncDispatched {
			continue
		}

		mod, ok := snap.Modules()[sub.ModuleName]
		if !ok {
			return fmt.Errorf("subscriber module %q is not loaded", sub.ModuleName)
		}

		insertOpts := &river.InsertOpts{
			Tags: []string{jobqueue.ModuleVersionTag(mod.Manifest.Version)},
			// Terminal jobs still deduplicate emitter retries after subscriber side effects.
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

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
