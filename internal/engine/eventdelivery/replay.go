package eventdelivery

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// replayEventRow is one matched event_log row — the columns a replay needs
// to reconstruct a SubscriberDeliveryArgs job, not the full row shape
// Worker's own INSERT writes.
type replayEventRow struct {
	ID            string
	EventName     string
	EmitterModule string
	Payload       []byte
	TraceID       sql.NullString
}

// matchingEventLogRows pages through qualifiedSlug's event_log table for
// rows matching eventNames/module/[from,to], ordered by emitted_at so
// repeated calls with increasing offset never skip or repeat a row
// between pages even as new events continue to be logged concurrently
// (an insert-only table growing at the tail doesn't shift earlier rows).
func matchingEventLogRows(ctx context.Context, pool *sql.DB, tenantSlug string, filter jobqueue.EventsReplayArgs, limit, offset int) ([]replayEventRow, error) {
	query := fmt.Sprintf(`
		SELECT id, event_name, emitter_module, payload, trace_id
		FROM %s.event_log
		WHERE event_name = ANY($1) AND ($2 = '' OR emitter_module = $2) AND emitted_at BETWEEN $3 AND $4
		ORDER BY emitted_at
		LIMIT $5 OFFSET $6
	`, tenantschema.Name(tenantSlug))
	rows, err := pool.QueryContext(ctx, query, pqStringArray(filter.EventNames), filter.Module, filter.From, filter.To, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query event_log: %w", err)
	}
	defer rows.Close()

	var out []replayEventRow
	for rows.Next() {
		var r replayEventRow
		if err := rows.Scan(&r.ID, &r.EventName, &r.EmitterModule, &r.Payload, &r.TraceID); err != nil {
			return nil, fmt.Errorf("scan event_log row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event_log rows: %w", err)
	}
	return out, nil
}

// pqStringArray formats a Go string slice as a Postgres text[] literal
// for the ANY($1) match above — database/sql has no native []string
// binding, and this codebase has no pq/pgtype array-encoding dependency
// wired into the plain *sql.DB pool other tenant-schema writes here
// already use (role.go/invite.go's own convention). Each element is
// double-quoted with backslash/quote escaping per Postgres's own array
// literal syntax — event names are manifest-declared, not free-form user
// input, but escaping costs nothing and avoids depending on that.
func pqStringArray(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		quoted[i] = `"` + escaped + `"`
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

// targetSubscribers resolves which of eventName's currently registered
// subscribers a replay should fan out to: async subscribers only (a sync
// subscriber has no job-based delivery path — sync dispatch happens
// inline at emit time, the same reasoning Worker.Work already applies),
// further narrowed to subscriberFilter's module names when it's
// non-empty ("--subscriber" omitted replays to every current async
// subscriber, cli-reference.md §8).
func targetSubscribers(snap *registry.RegistrySnapshot, eventName string, subscriberFilter []string) []event.EventSubscription {
	allowed := make(map[string]bool, len(subscriberFilter))
	for _, m := range subscriberFilter {
		allowed[m] = true
	}

	var out []event.EventSubscription
	for _, sub := range snap.EventRegistry().Subscribers(eventName) {
		if !sub.Async {
			continue
		}
		if len(subscriberFilter) > 0 && !allowed[sub.ModuleName] {
			continue
		}
		out = append(out, sub)
	}
	return out
}

// resolveReplayTenants expands filter.Tenant ("all", or a single slug)
// into the tenants a replay should process — every currently active
// tenant for "all" (tenant.Store.ActiveTenants), or just the one named
// slug (validated to exist via GetBySlug, so a typo'd slug fails fast
// with a clear error rather than silently matching zero event_log rows).
// Returns full *tenant.Tenant values, not bare slugs: SubscriberDeliveryArgs.TenantID
// must carry the tenant's real ID (matching Worker.Work's own
// EventDeliveryArgs.TenantID convention, ultimately modCtx.TenantID at
// emit time), not its schema-naming slug.
func resolveReplayTenants(ctx context.Context, tenantStore *tenant.Store, filterTenant string) ([]*tenant.Tenant, error) {
	if filterTenant == "all" {
		tenants, err := tenantStore.ActiveTenants(ctx)
		if err != nil {
			return nil, fmt.Errorf("list active tenants: %w", err)
		}
		out := make([]*tenant.Tenant, len(tenants))
		for i := range tenants {
			out[i] = &tenants[i]
		}
		return out, nil
	}

	t, err := tenantStore.GetBySlug(ctx, filterTenant)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant %q: %w", filterTenant, err)
	}
	return []*tenant.Tenant{t}, nil
}

// CountReplayMatches computes the matching-event and resulting-job counts
// a dry-run reports (cli-reference.md §8) without enqueueing anything —
// the read-only half of what EventsReplayWorker.Work does when it
// actually runs.
func CountReplayMatches(ctx context.Context, pool *sql.DB, moduleRegistry *registry.ModuleRegistry, tenantStore *tenant.Store, filter jobqueue.EventsReplayArgs) (eventCount, jobCount int, err error) {
	snap := moduleRegistry.Snapshot()
	if snap == nil {
		return 0, 0, fmt.Errorf("module registry has no snapshot yet")
	}

	tenants, err := resolveReplayTenants(ctx, tenantStore, filter.Tenant)
	if err != nil {
		return 0, 0, err
	}

	limit := filter.BatchSize
	for _, t := range tenants {
		offset := 0
		for {
			rows, err := matchingEventLogRows(ctx, pool, t.Slug, filter, limit, offset)
			if err != nil {
				return 0, 0, err
			}
			for _, r := range rows {
				eventCount++
				jobCount += len(targetSubscribers(snap, r.EventName, filter.Subscribers))
			}
			if len(rows) < limit {
				break
			}
			offset += limit
		}
	}
	return eventCount, jobCount, nil
}

// EstimatedDurationMinutes is a rough, operator-facing estimate only — no
// doc defines an exact formula, so this assumes one minute per batch of
// jobCount/batchSize jobs, rounded up, with a one-minute floor.
func EstimatedDurationMinutes(jobCount, batchSize int) int {
	if batchSize <= 0 {
		batchSize = 1
	}
	minutes := (jobCount + batchSize - 1) / batchSize
	return max(minutes, 1)
}

// EventsReplayWorker processes jobqueue.EventsReplayArgs jobs: for every
// tenant the filter resolves to, pages through matching event_log rows
// and enqueues one jobqueue.SubscriberDeliveryArgs job per matched-event/
// targeted-subscriber pair directly — it never re-inserts an
// EventDeliveryArgs job and never touches event_log itself, so a replay
// can never duplicate the original audit row. Each fan-out job carries
// the original event_log row's own id as EventID (for traceability back
// to the source event) and is UniqueOpts{ByArgs: true}-deduped exactly
// like Worker's own live-dispatch fan-out, so replaying the same range
// twice never double-enqueues.
type EventsReplayWorker struct {
	river.WorkerDefaults[jobqueue.EventsReplayArgs]
	ModuleRegistry *registry.ModuleRegistry
	TenantStore    *tenant.Store
	Pool           *sql.DB
}

func (w *EventsReplayWorker) Work(ctx context.Context, job *river.Job[jobqueue.EventsReplayArgs]) error {
	filter := job.Args

	snap := w.ModuleRegistry.Snapshot()
	if snap == nil {
		return fmt.Errorf("module registry has no snapshot yet")
	}

	tenants, err := resolveReplayTenants(ctx, w.TenantStore, filter.Tenant)
	if err != nil {
		return err
	}

	riverClient := river.ClientFromContext[pgx.Tx](ctx)
	limit := filter.BatchSize

	for _, t := range tenants {
		if err := replayTenant(ctx, riverClient, w.Pool, snap, t, filter, limit); err != nil {
			if filter.Tenant == "all" {
				// "protect the database": one tenant's failure never
				// blocks another's (event-system.md §10) — only
				// resolveReplayTenants' own enumeration failure above is
				// fatal to the whole job.
				log.Error().Err(err).Str("tenant", t.Slug).Msg("events replay: tenant failed, continuing")
				continue
			}
			return err
		}
	}
	return nil
}

func replayTenant(ctx context.Context, riverClient *river.Client[pgx.Tx], pool *sql.DB, snap *registry.RegistrySnapshot, t *tenant.Tenant, filter jobqueue.EventsReplayArgs, limit int) error {
	offset := 0
	for {
		rows, err := matchingEventLogRows(ctx, pool, t.Slug, filter, limit, offset)
		if err != nil {
			return err
		}

		var batch []river.InsertManyParams
		for _, r := range rows {
			for _, sub := range targetSubscribers(snap, r.EventName, filter.Subscribers) {
				batch = append(batch, river.InsertManyParams{
					Args: jobqueue.SubscriberDeliveryArgs{
						EventID:     r.ID,
						EventName:   r.EventName,
						ModuleName:  sub.ModuleName,
						HandlerName: sub.HandlerName,
						Payload:     r.Payload,
						TenantID:    t.ID,
						TraceID:     r.TraceID.String,
					},
					InsertOpts: &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}},
				})
			}
		}
		if len(batch) > 0 {
			if _, err := riverClient.InsertMany(ctx, batch); err != nil {
				return fmt.Errorf("enqueue subscriber delivery batch for tenant %q: %w", t.Slug, err)
			}
		}

		if len(rows) < limit {
			return nil
		}
		offset += limit
	}
}
