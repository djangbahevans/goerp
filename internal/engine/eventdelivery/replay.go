package eventdelivery

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

type replayEventRow struct {
	ID            string
	EventName     string
	EventVersion  int
	EmitterModule string
	Payload       []byte
	TraceID       sql.NullString
	UserID        sql.NullString
	EmittedAt     time.Time
}

func matchingEventLogRows(ctx context.Context, pool *sql.DB, tenantSlug string, filter jobqueue.EventsReplayArgs, limit, offset int) ([]replayEventRow, error) {
	query := fmt.Sprintf(`
		SELECT id, event_name, event_version, emitter_module, payload, trace_id, user_id, emitted_at
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
		if err := rows.Scan(&r.ID, &r.EventName, &r.EventVersion, &r.EmitterModule, &r.Payload, &r.TraceID, &r.UserID, &r.EmittedAt); err != nil {
			return nil, fmt.Errorf("scan event_log row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event_log rows: %w", err)
	}
	return out, nil
}

// database/sql binds Postgres arrays as text literals with escaped elements.
func pqStringArray(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		quoted[i] = `"` + escaped + `"`
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

// Synchronous subscribers run inline at emission and have no replay jobs.
func targetSubscribers(snap *registry.RegistrySnapshot, eventName string, version int, subscriberFilter []string) []event.EventSubscription {
	allowed := make(map[string]bool, len(subscriberFilter))
	for _, m := range subscriberFilter {
		allowed[m] = true
	}

	var out []event.EventSubscription
	for _, sub := range snap.EventRegistry().Subscribers(eventName, version) {
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
				jobCount += len(targetSubscribers(snap, r.EventName, r.EventVersion, filter.Subscribers))
			}
			if len(rows) < limit {
				break
			}
			offset += limit
		}
	}
	return eventCount, jobCount, nil
}

func EstimatedDurationMinutes(jobCount, batchSize int) int {
	if batchSize <= 0 {
		batchSize = 1
	}
	minutes := (jobCount + batchSize - 1) / batchSize
	return max(minutes, 1)
}

// Replays enqueue subscriber jobs directly to preserve the original event log entry.
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
			for _, sub := range targetSubscribers(snap, r.EventName, r.EventVersion, filter.Subscribers) {
				batch = append(batch, river.InsertManyParams{
					Args: jobqueue.SubscriberDeliveryArgs{
						EventID:       r.ID,
						EventName:     r.EventName,
						EventVersion:  r.EventVersion,
						EmitterModule: r.EmitterModule,
						ModuleName:    sub.ModuleName,
						HandlerName:   sub.HandlerName,
						Payload:       r.Payload,
						TenantID:      t.ID,
						TraceID:       r.TraceID.String,
						UserID:        r.UserID.String,
						EmittedAt:     r.EmittedAt,
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
