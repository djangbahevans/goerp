package eventdelivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
)

// localPostgresDSN/jobsTestDSN match the established constants in
// internal/engine/tenant/store_test.go (primary pool, direct port,
// bypassing PgBouncer) and internal/engine/tenant/offboard/
// offboarder_test.go (jobs pool, via PgBouncer) respectively.
const (
	localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
	jobsTestDSN      = "postgres://goerp:dev@localhost:6432/goerp"
)

func uniqueSlug(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("evtdelivery%d", time.Now().UnixNano())
}

// uniqueEventID returns a fresh random event ID per call, registering
// cleanup of any river_job row it ends up tagged on — a fixed literal ID
// reused across separate test runs against the same persistent dev
// Postgres previously caused genuine flakiness here: each run creates a
// brand-new tenant (a fresh random tenant_id), so a literal event_id
// reused across runs produced two real, legitimately-distinct
// SubscriberDeliveryArgs unique hashes (different tenant_id, same
// event_id) that both existed as leftover, never-cleaned-up rows,
// making a later run's own dedup assertion see a stale duplicate.
func uniqueEventID(t *testing.T, conn *sql.DB) string {
	t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, id)
	})
	return id
}

// newTestTenant creates a real, active tenant row plus a real
// "tenant_<slug>" schema containing just an event_log table — mirroring
// internal/engine/tenant/offboard/workflow_test.go's activeTenant, minus
// the files.Store bootstrap this package doesn't need.
func newTestTenant(t *testing.T, tenantStore *tenant.Store, conn *sql.DB, slug string) *tenant.Tenant {
	t.Helper()
	ctx := context.Background()

	tt, err := tenantStore.CreateTenant(ctx, slug, "Event Delivery Test")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID)
		_, _ = conn.Exec("DROP SCHEMA IF EXISTS " + tenantschema.Name(slug) + " CASCADE")
	})

	if _, err := tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+tenantschema.Name(slug)); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+tenantschema.Name(slug)+`.event_log (
		id             UUID NOT NULL DEFAULT uuidv7(),
		event_name     TEXT NOT NULL,
		event_version  INT NOT NULL DEFAULT 1,
		emitter_module TEXT NOT NULL,
		payload        BYTEA NOT NULL,
		trace_id       TEXT,
		user_id        UUID,
		emitted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (id, emitted_at)
	)`); err != nil {
		t.Fatalf("create event_log table: %v", err)
	}

	return tt
}

// newTestRiverClient builds a real, insert-only river.Client[pgx.Tx]
// against the real dev Postgres — mirroring runtime.go's own
// river.NewClient(driver, &river.Config{}) insert-only construction
// (host_event.go's insertClient uses the equivalent database/sql-driver
// shape). Never Start()'d: this test drives Worker.Work directly via
// rivertest.WorkContext rather than through a real queue poller, so
// there's no per-test queue-name isolation concern the way a Start()'d
// client sharing the literal "events" queue name across concurrent test
// processes would have.
func newTestRiverClient(t *testing.T) *river.Client[pgx.Tx] {
	t.Helper()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, jobsTestDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", jobsTestDSN, err)
	}
	t.Cleanup(pool.Close)

	if err := jobqueue.Migrate(ctx, pool); err != nil {
		t.Fatalf("jobqueue.Migrate: %v", err)
	}

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatalf("river.NewClient: %v", err)
	}
	return client
}

// newTestModuleRegistry builds a real *registry.ModuleRegistry whose
// EventRegistry has exactly the given subscriptions registered for
// eventName, going through the real ModuleRegistry.Update pipeline
// (registry_test.go's own established fixture-module pattern) rather
// than hand-assembling an *event.EventRegistry directly — RegistrySnapshot
// exposes no public constructor.
func newTestModuleRegistry(t *testing.T, eventName string, subs []manifest.EventSubscription) *registry.ModuleRegistry {
	t.Helper()

	reg := &registry.ModuleRegistry{}
	_, err := reg.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status: module.StatusReady,
			Manifest: manifest.Manifest{
				Type:       "standard",
				Subscribes: subs,
			},
		},
	})
	if err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}
	return reg
}

func newTestWorker(t *testing.T, eventName string, subs []manifest.EventSubscription) (*Worker, *tenant.Store, *sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant.Store.Bootstrap: %v", err)
	}

	riverClient := newTestRiverClient(t)
	ctx = rivertest.WorkContext(ctx, riverClient)

	w := &Worker{
		ModuleRegistry: newTestModuleRegistry(t, eventName, subs),
		TenantStore:    tenantStore,
		Pool:           conn,
	}
	return w, tenantStore, conn, ctx
}

func fanOutJobExists(t *testing.T, conn *sql.DB, eventID string) bool {
	t.Helper()
	var count int
	if err := conn.QueryRow(
		`SELECT count(*) FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`,
		eventID,
	).Scan(&count); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	return count > 0
}

type eventLogRow struct {
	EventName     string
	EventVersion  int
	EmitterModule string
	Payload       []byte
	TraceID       sql.NullString
	UserID        sql.NullString
}

func queryEventLog(t *testing.T, conn *sql.DB, slug, eventID string) []eventLogRow {
	t.Helper()
	rows, err := conn.Query(`SELECT event_name, event_version, emitter_module, payload, trace_id, user_id FROM `+tenantschema.Name(slug)+`.event_log WHERE id = $1`, eventID)
	if err != nil {
		t.Fatalf("query event_log: %v", err)
	}
	defer rows.Close()

	var out []eventLogRow
	for rows.Next() {
		var r eventLogRow
		if err := rows.Scan(&r.EventName, &r.EventVersion, &r.EmitterModule, &r.Payload, &r.TraceID, &r.UserID); err != nil {
			t.Fatalf("scan event_log row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate event_log rows: %v", err)
	}
	return out
}

func TestWork_AsyncSubscriber_EnqueuesFanOutJob(t *testing.T) {
	eventName := "sales.order.confirmed"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_order_confirmed", Async: true},
	})
	tt := newTestTenant(t, tenantStore, conn, uniqueSlug(t))

	eventID := uniqueEventID(t, conn)
	args := jobqueue.EventDeliveryArgs{
		EventID: eventID, EventName: eventName, EventVersion: 1,
		EmitterModule: "sales", TenantID: tt.ID, Payload: mustMarshal(t, map[string]any{"id": 1}),
	}
	if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	if !fanOutJobExists(t, conn, eventID) {
		t.Error("expected a subscriber_delivery job to be enqueued for the async subscriber")
	}
}

// TestWork_SyncSubscriber_FallsBackToAsyncWhenNotSyncDispatched proves
// event-system.md §8's documented footgun fallback: an async:false
// subscriber whose emission never actually dispatched it synchronously
// (SyncDispatched unset — a plain Emit, or the EmitTx case that rejects
// WithSync() outright) still needs delivering, just asynchronously
// instead of being silently dropped.
func TestWork_SyncSubscriber_FallsBackToAsyncWhenNotSyncDispatched(t *testing.T) {
	eventName := "sales.order.shipped"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_order_shipped", Async: false},
	})
	tt := newTestTenant(t, tenantStore, conn, uniqueSlug(t))

	eventID := uniqueEventID(t, conn)
	args := jobqueue.EventDeliveryArgs{
		EventID: eventID, EventName: eventName, EventVersion: 1,
		EmitterModule: "sales", TenantID: tt.ID, Payload: mustMarshal(t, map[string]any{}),
		SyncDispatched: false,
	}
	if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	if !fanOutJobExists(t, conn, eventID) {
		t.Error("expected a fallback subscriber_delivery job for a sync-only subscriber not actually dispatched synchronously")
	}
}

func TestWork_SyncSubscriber_NoFanOutJobWhenAlreadySyncDispatched(t *testing.T) {
	eventName := "sales.order.shipped"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_order_shipped", Async: false},
	})
	tt := newTestTenant(t, tenantStore, conn, uniqueSlug(t))

	eventID := uniqueEventID(t, conn)
	args := jobqueue.EventDeliveryArgs{
		EventID: eventID, EventName: eventName, EventVersion: 1,
		EmitterModule: "sales", TenantID: tt.ID, Payload: mustMarshal(t, map[string]any{}),
		SyncDispatched: true,
	}
	if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	if fanOutJobExists(t, conn, eventID) {
		t.Error("expected no subscriber_delivery job for a sync-only subscriber already dispatched inline")
	}
}

func TestWork_MaxAttemptsSetFromSubscriberRetryPolicy(t *testing.T) {
	eventName := "sales.order.confirmed"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, []manifest.EventSubscription{
		{
			Name: eventName, Handler: "handle_order_confirmed", Async: true,
			RetryPolicy: &manifest.RetryPolicy{MaxAttempts: 7, Backoff: "linear", InitialDelayMS: 1000},
		},
	})
	tt := newTestTenant(t, tenantStore, conn, uniqueSlug(t))

	eventID := uniqueEventID(t, conn)
	args := jobqueue.EventDeliveryArgs{
		EventID: eventID, EventName: eventName, EventVersion: 1,
		EmitterModule: "sales", TenantID: tt.ID, Payload: mustMarshal(t, map[string]any{}),
	}
	if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	var maxAttempts int
	if err := conn.QueryRow(`SELECT max_attempts FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID).Scan(&maxAttempts); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	if maxAttempts != 7 {
		t.Errorf("max_attempts = %d, want 7 (from the subscriber's own retry_policy)", maxAttempts)
	}
}

func TestWork_WritesEventLogRow(t *testing.T) {
	eventName := "sales.order.cancelled"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, nil)
	slug := uniqueSlug(t)
	tt := newTestTenant(t, tenantStore, conn, slug)

	eventID := uniqueEventID(t, conn)
	args := jobqueue.EventDeliveryArgs{
		EventID: eventID, EventName: eventName, EventVersion: 2,
		EmitterModule: "sales", TenantID: tt.ID, TraceID: "trace-abc",
		Payload: mustMarshal(t, map[string]any{"reason": "customer request"}),
	}
	if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	rows := queryEventLog(t, conn, slug, eventID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 event_log row, got %d", len(rows))
	}
	row := rows[0]
	if row.EventName != eventName || row.EventVersion != 2 || row.EmitterModule != "sales" {
		t.Errorf("unexpected event_log row: %+v", row)
	}
	if !row.TraceID.Valid || row.TraceID.String != "trace-abc" {
		t.Errorf("trace_id = %+v, want trace-abc", row.TraceID)
	}
	if row.UserID.Valid {
		t.Errorf("expected user_id NULL, got %+v", row.UserID)
	}
}

func TestWork_RetryIsIdempotent(t *testing.T) {
	eventName := "sales.order.updated"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_order_updated", Async: true},
	})
	slug := uniqueSlug(t)
	tt := newTestTenant(t, tenantStore, conn, slug)

	eventID := uniqueEventID(t, conn)
	args := jobqueue.EventDeliveryArgs{
		EventID: eventID, EventName: eventName, EventVersion: 1,
		EmitterModule: "sales", TenantID: tt.ID, Payload: mustMarshal(t, map[string]any{}),
	}
	for range 2 {
		if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
			t.Fatalf("Work: %v", err)
		}
	}

	if rows := queryEventLog(t, conn, slug, eventID); len(rows) != 1 {
		t.Fatalf("expected exactly 1 event_log row after 2 Work() calls, got %d", len(rows))
	}
	var fanOutCount int
	if err := conn.QueryRow(`SELECT count(*) FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID).Scan(&fanOutCount); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	if fanOutCount != 1 {
		t.Errorf("expected exactly 1 fan-out job after 2 Work() calls, got %d", fanOutCount)
	}
}

// TestWork_RetryAfterSubscriberJobTerminalDoesNotReExecute proves the
// acceptance criterion behind the ByState fix (jobqueue.
// UniqueAcrossAllJobStates): a retried EventDeliveryArgs delivery (the
// same EventID, as if the emitter's own transaction were retried) must
// not insert a second SubscriberDeliveryArgs job once the first one has
// already reached a terminal state — River's own "active"-only default
// ByState would let a second job through here, invoking the subscriber's
// handler side effects again.
func TestWork_RetryAfterSubscriberJobTerminalDoesNotReExecute(t *testing.T) {
	eventName := "sales.order.updated"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, []manifest.EventSubscription{
		{Name: eventName, Handler: "handle_order_updated", Async: true},
	})
	slug := uniqueSlug(t)
	tt := newTestTenant(t, tenantStore, conn, slug)

	eventID := uniqueEventID(t, conn)
	args := jobqueue.EventDeliveryArgs{
		EventID: eventID, EventName: eventName, EventVersion: 1,
		EmitterModule: "sales", TenantID: tt.ID, Payload: mustMarshal(t, map[string]any{}),
	}
	if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
		t.Fatalf("Work (first delivery): %v", err)
	}

	// Simulate the subscriber_delivery job having already been fully
	// processed (River's real terminal state after SubscriberDeliveryWorker
	// succeeds) before the emitter's own retry arrives.
	if _, err := conn.Exec(`UPDATE river_job SET state = 'completed', finalized_at = now() WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID); err != nil {
		t.Fatalf("mark subscriber_delivery job completed: %v", err)
	}

	if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
		t.Fatalf("Work (retried delivery): %v", err)
	}

	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, eventID).Scan(&count); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 subscriber_delivery job after a retry past a terminal state, got %d", count)
	}
}

func TestWork_TenantIsolation(t *testing.T) {
	eventName := "sales.order.confirmed"
	w, tenantStore, conn, ctx := newTestWorker(t, eventName, nil)
	slugA, slugB := uniqueSlug(t), uniqueSlug(t)
	ttA := newTestTenant(t, tenantStore, conn, slugA)
	ttB := newTestTenant(t, tenantStore, conn, slugB)

	eventIDA := uniqueEventID(t, conn)
	eventIDB := uniqueEventID(t, conn)
	for _, args := range []jobqueue.EventDeliveryArgs{
		{EventID: eventIDA, EventName: eventName, EmitterModule: "sales", TenantID: ttA.ID, Payload: mustMarshal(t, map[string]any{})},
		{EventID: eventIDB, EventName: eventName, EmitterModule: "sales", TenantID: ttB.ID, Payload: mustMarshal(t, map[string]any{})},
	} {
		if err := w.Work(ctx, &river.Job[jobqueue.EventDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
			t.Fatalf("Work: %v", err)
		}
	}

	if rows := queryEventLog(t, conn, slugA, eventIDA); len(rows) != 1 {
		t.Fatalf("tenant A: expected its own event_log row, got %d", len(rows))
	}
	if rows := queryEventLog(t, conn, slugA, eventIDB); len(rows) != 0 {
		t.Fatalf("tenant A's event_log leaked tenant B's row: %+v", rows)
	}
	if rows := queryEventLog(t, conn, slugB, eventIDB); len(rows) != 1 {
		t.Fatalf("tenant B: expected its own event_log row, got %d", len(rows))
	}
	if rows := queryEventLog(t, conn, slugB, eventIDA); len(rows) != 0 {
		t.Fatalf("tenant B's event_log leaked tenant A's row: %+v", rows)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}
