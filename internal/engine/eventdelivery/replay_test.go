package eventdelivery

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// insertFixtureEventLogRow inserts one event_log row directly, bypassing
// Worker entirely — replay tests need pre-existing historical rows to
// match against, not a live dispatch.
func insertFixtureEventLogRow(t *testing.T, conn *sql.DB, slug, id, eventName, emitterModule string, emittedAt time.Time) {
	t.Helper()
	_, err := conn.ExecContext(context.Background(), `
		INSERT INTO `+tenantschema.Name(slug)+`.event_log (id, event_name, emitter_module, payload, emitted_at)
		VALUES ($1, $2, $3, $4, $5)
	`, id, eventName, emitterModule, []byte(`{}`), emittedAt)
	if err != nil {
		t.Fatalf("insert fixture event_log row: %v", err)
	}
}

func TestMatchingEventLogRows_FiltersByEventNameModuleAndTimeRange(t *testing.T) {
	_, tenantStore, conn, _ := newTestWorker(t, "sales.order.confirmed", nil)
	slug := uniqueSlug(t)
	newTestTenant(t, tenantStore, conn, slug)

	now := time.Now().UTC().Truncate(time.Second)
	inRange := uuid.Must(uuid.NewV7()).String()
	wrongEvent := uuid.Must(uuid.NewV7()).String()
	wrongModule := uuid.Must(uuid.NewV7()).String()
	outOfRange := uuid.Must(uuid.NewV7()).String()

	insertFixtureEventLogRow(t, conn, slug, inRange, "sales.order.confirmed", "sales", now)
	insertFixtureEventLogRow(t, conn, slug, wrongEvent, "sales.order.cancelled", "sales", now)
	insertFixtureEventLogRow(t, conn, slug, wrongModule, "sales.order.confirmed", "inventory", now)
	insertFixtureEventLogRow(t, conn, slug, outOfRange, "sales.order.confirmed", "sales", now.Add(-48*time.Hour))

	filter := jobqueue.EventsReplayArgs{
		EventNames: []string{"sales.order.confirmed"},
		Module:     "sales",
		From:       now.Add(-time.Hour),
		To:         now.Add(time.Hour),
	}
	rows, err := matchingEventLogRows(context.Background(), conn, slug, filter, 100, 0)
	if err != nil {
		t.Fatalf("matchingEventLogRows: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != inRange {
		t.Fatalf("expected only the in-range matching row, got %+v", rows)
	}
}

func TestTargetSubscribers_AsyncOnlyAndSubscriberFilter(t *testing.T) {
	eventName := "sales.order.confirmed"
	reg := newTestModuleRegistry(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_async_a", Async: true},
		{Name: eventName, Handler: "handle_sync", Async: false},
	})
	snap := reg.Snapshot()

	subs := targetSubscribers(snap, eventName, nil)
	if len(subs) != 1 || subs[0].HandlerName != "handle_async_a" {
		t.Fatalf("expected only the async subscriber with no filter, got %+v", subs)
	}

	subsFiltered := targetSubscribers(snap, eventName, []string{"nonexistent-module"})
	if len(subsFiltered) != 0 {
		t.Fatalf("expected no subscribers for a non-matching module filter, got %+v", subsFiltered)
	}
}

func TestCountReplayMatches_SingleTenant(t *testing.T) {
	eventName := "sales.order.confirmed"
	w, tenantStore, conn, _ := newTestWorker(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_a", Async: true},
		{Name: eventName, Handler: "handle_b", Async: true},
	})
	slug := uniqueSlug(t)
	newTestTenant(t, tenantStore, conn, slug)

	now := time.Now().UTC().Truncate(time.Second)
	insertFixtureEventLogRow(t, conn, slug, uuid.Must(uuid.NewV7()).String(), eventName, "sales", now)
	insertFixtureEventLogRow(t, conn, slug, uuid.Must(uuid.NewV7()).String(), eventName, "sales", now)

	filter := jobqueue.EventsReplayArgs{
		Tenant:     slug,
		EventNames: []string{eventName},
		From:       now.Add(-time.Hour),
		To:         now.Add(time.Hour),
		BatchSize:  100,
	}
	eventCount, jobCount, err := CountReplayMatches(context.Background(), conn, w.ModuleRegistry, tenantStore, filter)
	if err != nil {
		t.Fatalf("CountReplayMatches: %v", err)
	}
	if eventCount != 2 {
		t.Errorf("eventCount = %d, want 2", eventCount)
	}
	if jobCount != 4 {
		t.Errorf("jobCount = %d, want 4 (2 events x 2 async subscribers)", jobCount)
	}
}

func TestEventsReplayWorker_Work_EnqueuesFanOutJobsForMatchedEvents(t *testing.T) {
	eventName := "sales.order.confirmed"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_replay", Async: true},
	})
	slug := uniqueSlug(t)
	tt := newTestTenant(t, tenantStore, conn, slug)

	now := time.Now().UTC().Truncate(time.Second)
	eventID := uuid.Must(uuid.NewV7()).String()
	insertFixtureEventLogRow(t, conn, slug, eventID, eventName, "sales", now)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID)
	})

	replayWorker := &EventsReplayWorker{ModuleRegistry: w.ModuleRegistry, TenantStore: tenantStore, Pool: conn}
	job := &river.Job[jobqueue.EventsReplayArgs]{JobRow: &rivertype.JobRow{}, Args: jobqueue.EventsReplayArgs{
		Tenant:     slug,
		EventNames: []string{eventName},
		From:       now.Add(-time.Hour),
		To:         now.Add(time.Hour),
		BatchSize:  100,
	}}
	if err := replayWorker.Work(ctx, job); err != nil {
		t.Fatalf("Work: %v", err)
	}

	var count int
	var tenantID string
	if err := conn.QueryRow(`SELECT count(*), max(args->>'tenant_id') FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID).Scan(&count, &tenantID); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 fan-out job, got %d", count)
	}
	if tenantID != tt.ID {
		t.Errorf("fan-out job tenant_id = %q, want the tenant's real ID %q", tenantID, tt.ID)
	}
}

func TestEventsReplayWorker_Work_DoesNotDoubleEnqueueOnRetry(t *testing.T) {
	eventName := "sales.order.confirmed"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_replay_retry", Async: true},
	})
	slug := uniqueSlug(t)
	newTestTenant(t, tenantStore, conn, slug)

	now := time.Now().UTC().Truncate(time.Second)
	eventID := uuid.Must(uuid.NewV7()).String()
	insertFixtureEventLogRow(t, conn, slug, eventID, eventName, "sales", now)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID)
	})

	replayWorker := &EventsReplayWorker{ModuleRegistry: w.ModuleRegistry, TenantStore: tenantStore, Pool: conn}
	args := jobqueue.EventsReplayArgs{
		Tenant:     slug,
		EventNames: []string{eventName},
		From:       now.Add(-time.Hour),
		To:         now.Add(time.Hour),
		BatchSize:  100,
	}
	for range 2 {
		job := &river.Job[jobqueue.EventsReplayArgs]{JobRow: &rivertype.JobRow{}, Args: args}
		if err := replayWorker.Work(ctx, job); err != nil {
			t.Fatalf("Work: %v", err)
		}
	}

	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID).Scan(&count); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 fan-out job after 2 replay Work() calls, got %d", count)
	}
}

// TestEventsReplayWorker_Work_TenantAll covers --tenant all resolving via
// tenant.Store.ActiveTenants rather than a single named slug — the
// fixture tenant newTestTenant creates is already StatusActive, so it's
// picked up by the "all" enumeration same as any other active tenant.
func TestEventsReplayWorker_Work_TenantAll(t *testing.T) {
	eventName := "sales.order.confirmed"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_replay_all", Async: true},
	})
	slug := uniqueSlug(t)
	newTestTenant(t, tenantStore, conn, slug)

	now := time.Now().UTC().Truncate(time.Second)
	eventID := uuid.Must(uuid.NewV7()).String()
	insertFixtureEventLogRow(t, conn, slug, eventID, eventName, "sales", now)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID)
	})

	replayWorker := &EventsReplayWorker{ModuleRegistry: w.ModuleRegistry, TenantStore: tenantStore, Pool: conn}
	job := &river.Job[jobqueue.EventsReplayArgs]{JobRow: &rivertype.JobRow{}, Args: jobqueue.EventsReplayArgs{
		Tenant:     "all",
		EventNames: []string{eventName},
		From:       now.Add(-time.Hour),
		To:         now.Add(time.Hour),
		BatchSize:  100,
	}}
	if err := replayWorker.Work(ctx, job); err != nil {
		t.Fatalf("Work: %v", err)
	}

	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID).Scan(&count); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 fan-out job replaying across all active tenants, got %d", count)
	}
}
