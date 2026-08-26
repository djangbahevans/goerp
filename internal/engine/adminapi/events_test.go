package adminapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// newInsertOnlyEventsJobClient builds a real, insert-only
// river.Client[pgx.Tx] against the dev jobs Postgres — never Start()'d,
// matching internal/engine/eventdelivery's own test-fixture pattern
// exactly. jobs_test.go's shared newTestJobsClient is a STARTED client
// registered only with ProbeWorker, which would immediately try (and
// fail) to work an inserted events_replay job — this route's own tests
// only need Insert to succeed and return a job_id, not for the job to
// actually run.
func newInsertOnlyEventsJobClient(t *testing.T) *river.Client[pgx.Tx] {
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

const eventsLocalPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// newTestEventsDeps builds a real EventsDeps against the dev stack: a
// primary pool, a real active tenant with a fixture event_log table, a
// module registry with one async subscriber for eventName, and an
// insert-only river.Client[pgx.Tx] (jobsTestDSN/newTestJobsClient come
// from jobs_test.go, same package) — mirrors
// internal/engine/eventdelivery's own test fixtures, rebuilt locally
// here since adminapi is a different package.
func newTestEventsDeps(t *testing.T, eventName string) (EventsDeps, string) {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(eventsLocalPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", eventsLocalPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant.Store.Bootstrap: %v", err)
	}

	slug := fmt.Sprintf("evtreplayapi%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Events Replay API Test")
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
		id             UUID PRIMARY KEY DEFAULT uuidv7(),
		event_name     TEXT NOT NULL,
		event_version  INT NOT NULL DEFAULT 1,
		emitter_module TEXT NOT NULL,
		payload        BYTEA NOT NULL,
		trace_id       TEXT,
		user_id        UUID,
		emitted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		t.Fatalf("create event_log table: %v", err)
	}

	moduleRegistry := &registry.ModuleRegistry{}
	if _, err := moduleRegistry.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status: module.StatusReady,
			Manifest: manifest.Manifest{
				Type: "standard",
				Subscribes: []manifest.EventSubscription{
					{Name: eventName, Handler: "handle_replay", Async: true},
				},
			},
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}

	jobClient := newInsertOnlyEventsJobClient(t)

	return EventsDeps{
		ModuleRegistry: moduleRegistry,
		TenantStore:    tenantStore,
		Pool:           conn,
		JobClient:      jobClient,
	}, slug
}

func insertFixtureEventLogRowForAPI(t *testing.T, conn *sql.DB, slug, eventName string) {
	t.Helper()
	_, err := conn.ExecContext(context.Background(), `
		INSERT INTO `+tenantschema.Name(slug)+`.event_log (event_name, emitter_module, payload)
		VALUES ($1, $2, $3)
	`, eventName, "testmodule", []byte(`{}`))
	if err != nil {
		t.Fatalf("insert fixture event_log row: %v", err)
	}
}

func TestEventsReplayRoute_DryRunReturnsCounts(t *testing.T) {
	eventName := "sales.order.confirmed"
	deps, slug := newTestEventsDeps(t, eventName)
	insertFixtureEventLogRowForAPI(t, deps.Pool, slug, eventName)
	insertFixtureEventLogRowForAPI(t, deps.Pool, slug, eventName)

	mux := http.NewServeMux()
	RegisterEventsRoutes(mux, deps)

	body, _ := json.Marshal(map[string]any{
		"tenant":  slug,
		"event":   []string{eventName},
		"from":    time.Now().Add(-time.Hour).Format(time.RFC3339),
		"dry_run": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/events/replay", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var env struct {
		Data replayDryRunResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.EventCount != 2 {
		t.Errorf("EventCount = %d, want 2", env.Data.EventCount)
	}
	if env.Data.JobCount != 2 {
		t.Errorf("JobCount = %d, want 2 (2 events x 1 async subscriber)", env.Data.JobCount)
	}
}

func TestEventsReplayRoute_MissingConfirmOnBroadTargetIsBadRequest(t *testing.T) {
	eventName := "sales.order.confirmed"
	deps, slug := newTestEventsDeps(t, eventName)

	mux := http.NewServeMux()
	RegisterEventsRoutes(mux, deps)

	// --subscriber omitted -> broad target -> confirm required.
	body, _ := json.Marshal(map[string]any{
		"tenant": slug,
		"event":  []string{eventName},
		"from":   time.Now().Add(-time.Hour).Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/events/replay", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want %d", w.Code, w.Body.String(), http.StatusBadRequest)
	}
}

func TestEventsReplayRoute_WrongConfirmValueIsBadRequest(t *testing.T) {
	eventName := "sales.order.confirmed"
	deps, slug := newTestEventsDeps(t, eventName)

	mux := http.NewServeMux()
	RegisterEventsRoutes(mux, deps)

	body, _ := json.Marshal(map[string]any{
		"tenant":  slug,
		"event":   []string{eventName},
		"from":    time.Now().Add(-time.Hour).Format(time.RFC3339),
		"confirm": "yes",
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/events/replay", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want %d", w.Code, w.Body.String(), http.StatusBadRequest)
	}
}

func TestEventsReplayRoute_ScopedSubscriberSkipsConfirmAndInsertsJob(t *testing.T) {
	eventName := "sales.order.confirmed"
	deps, slug := newTestEventsDeps(t, eventName)

	mux := http.NewServeMux()
	RegisterEventsRoutes(mux, deps)

	// --subscriber scoped to one module, --tenant a single slug -> not a
	// broad target -> no confirm required.
	body, _ := json.Marshal(map[string]any{
		"tenant":     slug,
		"event":      []string{eventName},
		"subscriber": []string{"testmodule"},
		"from":       time.Now().Add(-time.Hour).Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/events/replay", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s, want %d", w.Code, w.Body.String(), http.StatusAccepted)
	}
	var env struct {
		Data struct {
			JobID     string `json:"job_id"`
			StatusURL string `json:"status_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.JobID == "" || env.Data.StatusURL != "/admin/jobs/"+env.Data.JobID {
		t.Errorf("unexpected response: %+v", env.Data)
	}
}

func TestEventsReplayRoute_DryRunUnknownTenantIsNotFound(t *testing.T) {
	eventName := "sales.order.confirmed"
	deps, _ := newTestEventsDeps(t, eventName)

	mux := http.NewServeMux()
	RegisterEventsRoutes(mux, deps)

	body, _ := json.Marshal(map[string]any{
		"tenant":  "nonexistent-tenant-slug",
		"event":   []string{eventName},
		"from":    time.Now().Add(-time.Hour).Format(time.RFC3339),
		"dry_run": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/events/replay", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s, want %d", w.Code, w.Body.String(), http.StatusNotFound)
	}
}

func TestEventsReplayRoute_MissingRequiredFieldsIsBadRequest(t *testing.T) {
	eventName := "sales.order.confirmed"
	deps, _ := newTestEventsDeps(t, eventName)

	mux := http.NewServeMux()
	RegisterEventsRoutes(mux, deps)

	body, _ := json.Marshal(map[string]any{"event": []string{eventName}})
	req := httptest.NewRequest(http.MethodPost, "/admin/events/replay", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want %d", w.Code, w.Body.String(), http.StatusBadRequest)
	}
}
