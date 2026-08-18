package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

const jobsTestDSN = "postgres://goerp:dev@localhost:6432/goerp"

func newTestJobsClient(t *testing.T) *river.Client[pgx.Tx] {
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
		t.Fatalf("Migrate: %v", err)
	}

	cfg := &config.Config{
		QueueCriticalConcurrency: 1, QueueDefaultConcurrency: 1,
		QueueBulkConcurrency: 1, QueueSearchConcurrency: 1, QueueEmailConcurrency: 1,
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &jobqueue.ProbeWorker{})

	client, err := jobqueue.New(pool, cfg, workers)
	if err != nil {
		t.Fatalf("jobqueue.New: %v", err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	})

	return client
}

func insertTestJob(t *testing.T, client *river.Client[pgx.Tx], opts *river.InsertOpts) int64 {
	t.Helper()
	row, err := client.Insert(context.Background(), jobqueue.ProbeArgs{
		IdempotencyKey: t.Name() + "-" + time.Now().String(),
		Message:        "adminapi jobs test",
	}, opts)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	return row.Job.ID
}

func TestEncodeDecodeJobID_RoundTrips(t *testing.T) {
	id, err := decodeJobID(encodeJobID(42))
	if err != nil {
		t.Fatalf("decodeJobID: %v", err)
	}
	if id != 42 {
		t.Errorf("id = %d, want 42", id)
	}
}

func TestDecodeJobID_RejectsMissingPrefix(t *testing.T) {
	if _, err := decodeJobID("42"); err == nil {
		t.Fatal("expected an error for a job id missing the job_ prefix")
	}
}

func TestDecodeJobID_RejectsNonNumeric(t *testing.T) {
	if _, err := decodeJobID("job_abc"); err == nil {
		t.Fatal("expected an error for a non-numeric job id")
	}
}

func TestJobsListRoute_ReturnsInsertedJob(t *testing.T) {
	client := newTestJobsClient(t)
	jobID := insertTestJob(t, client, &river.InsertOpts{Queue: jobqueue.QueueDefault})

	mux := http.NewServeMux()
	RegisterJobsRoutes(mux, JobsDeps{Client: client})

	req := httptest.NewRequest(http.MethodGet, "/admin/jobs?queue=default&limit=50", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var env struct {
		Data []jobView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	wantID := encodeJobID(jobID)
	for _, j := range env.Data {
		if j.ID == wantID {
			return
		}
	}
	t.Errorf("job %s not found in list response: %+v", wantID, env.Data)
}

func TestJobsListRoute_SinceExcludesOlderJobs(t *testing.T) {
	client := newTestJobsClient(t)
	jobID := insertTestJob(t, client, &river.InsertOpts{Queue: jobqueue.QueueDefault})

	mux := http.NewServeMux()
	RegisterJobsRoutes(mux, JobsDeps{Client: client})

	// A since window so small the just-inserted job can plausibly fall
	// outside it lands on very slow CI runs — 24h is deliberately large
	// enough that "excluded" here can only mean the filter is inverted,
	// not a timing fluke.
	req := httptest.NewRequest(http.MethodGet, "/admin/jobs?since=-24h", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var env struct {
		Data []jobView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	wantID := encodeJobID(jobID)
	for _, j := range env.Data {
		if j.ID == wantID {
			t.Errorf("job %s found with since=-24h (i.e. a future cutoff), want it excluded", wantID)
		}
	}
}

func TestJobsListRoute_InvalidSinceIsBadRequest(t *testing.T) {
	client := newTestJobsClient(t)

	mux := http.NewServeMux()
	RegisterJobsRoutes(mux, JobsDeps{Client: client})

	req := httptest.NewRequest(http.MethodGet, "/admin/jobs?since=not-a-duration", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestJobsShowRoute_ReturnsJobWithErrors(t *testing.T) {
	client := newTestJobsClient(t)
	jobID := insertTestJob(t, client, &river.InsertOpts{Queue: jobqueue.QueueDefault})

	mux := http.NewServeMux()
	RegisterJobsRoutes(mux, JobsDeps{Client: client})

	req := httptest.NewRequest(http.MethodGet, "/admin/jobs/"+encodeJobID(jobID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var env struct {
		Data jobDetailView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.ID != encodeJobID(jobID) {
		t.Errorf("ID = %q, want %q", env.Data.ID, encodeJobID(jobID))
	}
	if env.Data.Errors == nil {
		t.Error("Errors = nil, want an empty (but present) slice for a job with no failed attempts")
	}
}

func TestJobsShowRoute_UnknownJobReturnsNotFound(t *testing.T) {
	client := newTestJobsClient(t)

	mux := http.NewServeMux()
	RegisterJobsRoutes(mux, JobsDeps{Client: client})

	req := httptest.NewRequest(http.MethodGet, "/admin/jobs/job_999999999", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestJobsShowRoute_MalformedIDIsBadRequest(t *testing.T) {
	client := newTestJobsClient(t)

	mux := http.NewServeMux()
	RegisterJobsRoutes(mux, JobsDeps{Client: client})

	req := httptest.NewRequest(http.MethodGet, "/admin/jobs/not-a-job-id", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestJobsCancelRoute_CancelsJob(t *testing.T) {
	client := newTestJobsClient(t)
	// Scheduled far in the future so it's still cancellable (not already
	// picked up and completed) by the time the request runs.
	jobID := insertTestJob(t, client, &river.InsertOpts{
		Queue:       jobqueue.QueueDefault,
		ScheduledAt: time.Now().Add(time.Hour),
	})

	mux := http.NewServeMux()
	RegisterJobsRoutes(mux, JobsDeps{Client: client})

	req := httptest.NewRequest(http.MethodPost, "/admin/jobs/"+encodeJobID(jobID)+"/cancel", strings.NewReader(`{"reason":"test"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var env struct {
		Data jobView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.State != "cancelled" {
		t.Errorf("State = %q, want cancelled", env.Data.State)
	}
}

func TestJobsRetryRoute_RetriesJob(t *testing.T) {
	client := newTestJobsClient(t)
	jobID := insertTestJob(t, client, &river.InsertOpts{
		Queue:       jobqueue.QueueDefault,
		ScheduledAt: time.Now().Add(time.Hour),
	})

	mux := http.NewServeMux()
	RegisterJobsRoutes(mux, JobsDeps{Client: client})

	req := httptest.NewRequest(http.MethodPost, "/admin/jobs/"+encodeJobID(jobID)+"/retry", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var env struct {
		Data jobView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.State != "available" {
		t.Errorf("State = %q, want available (retry bypasses the schedule)", env.Data.State)
	}
}
