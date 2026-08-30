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
	"github.com/riverqueue/river/rivertype"
)

const jobsTestDSN = "postgres://goerp:dev@localhost:6432/goerp"

// newTestJobsClient uses jobqueue.NewIsolated, not jobqueue.New: this
// package's own tests and every other package's River-backed tests run as
// separate concurrent processes against the same shared dev Postgres, all
// registering New's fixed, package-level queue names (QueueDefault,
// QueueBulk, ...) with no per-process scoping — any one of them can poll
// and claim a job another one enqueued, and a client whose own Workers
// doesn't know that job's kind leaves it permanently retryable instead of
// completed. See jobqueue.NewIsolated's own doc comment.
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

	cfg := &config.Config{
		QueueCriticalConcurrency: 1, QueueDefaultConcurrency: 1,
		QueueBulkConcurrency: 1, QueueSearchConcurrency: 1, QueueEmailConcurrency: 1,
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &jobqueue.ProbeWorker{})

	client, err := jobqueue.NewIsolated(ctx, t, pool, cfg, workers)
	if err != nil {
		t.Fatalf("jobqueue.NewIsolated: %v", err)
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

	wantID := jobqueue.EncodeJobID(jobID)
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

	wantID := jobqueue.EncodeJobID(jobID)
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

	req := httptest.NewRequest(http.MethodGet, "/admin/jobs/"+jobqueue.EncodeJobID(jobID), nil)
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
	if env.Data.ID != jobqueue.EncodeJobID(jobID) {
		t.Errorf("ID = %q, want %q", env.Data.ID, jobqueue.EncodeJobID(jobID))
	}
	if env.Data.Errors == nil {
		t.Error("Errors = nil, want an empty (but present) slice for a job with no failed attempts")
	}
}

// outputTestArgs/outputTestWorker are a minimal job kind that records
// Output via river.RecordOutput — ProbeWorker (newTestJobsClient's only
// registered worker) never does, so OutputDecryptor's own test needs its
// own client with this worker registered too.
type outputTestArgs struct {
	Marker string
}

func (outputTestArgs) Kind() string { return "adminapi_test.output" }

type outputTestResult struct {
	Marker string `json:"marker"`
}

type outputTestWorker struct {
	river.WorkerDefaults[outputTestArgs]
}

func (w *outputTestWorker) Work(ctx context.Context, job *river.Job[outputTestArgs]) error {
	return river.RecordOutput(ctx, outputTestResult{Marker: job.Args.Marker})
}

// newTestJobsClientWithOutputWorker uses jobqueue.NewIsolated — see
// newTestJobsClient's own doc comment for why.
func newTestJobsClientWithOutputWorker(t *testing.T) *river.Client[pgx.Tx] {
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

	cfg := &config.Config{
		QueueCriticalConcurrency: 1, QueueDefaultConcurrency: 1,
		QueueBulkConcurrency: 1, QueueSearchConcurrency: 1, QueueEmailConcurrency: 1,
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &outputTestWorker{})

	client, err := jobqueue.NewIsolated(ctx, t, pool, cfg, workers)
	if err != nil {
		t.Fatalf("jobqueue.NewIsolated: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	})

	return client
}

// waitForJobCompleted polls JobGet until the job reaches rivertype.JobStateCompleted.
func waitForJobCompleted(t *testing.T, client *river.Client[pgx.Tx], jobID int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		row, err := client.JobGet(context.Background(), jobID)
		if err != nil {
			t.Fatalf("JobGet: %v", err)
		}
		if row.State == rivertype.JobStateCompleted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d did not complete in time (state=%s)", jobID, row.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestJobsShowRoute_AppliesOutputDecryptor(t *testing.T) {
	client := newTestJobsClientWithOutputWorker(t)

	insertResult, err := client.Insert(context.Background(), outputTestArgs{Marker: "encrypted-marker"}, &river.InsertOpts{Queue: jobqueue.QueueDefault})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	jobID := insertResult.Job.ID
	waitForJobCompleted(t, client, jobID)

	mux := http.NewServeMux()
	var gotKind string
	RegisterJobsRoutes(mux, JobsDeps{
		Client: client,
		OutputDecryptor: func(kind string, output json.RawMessage) (json.RawMessage, error) {
			gotKind = kind
			var result outputTestResult
			if err := json.Unmarshal(output, &result); err != nil {
				return nil, err
			}
			result.Marker = "decrypted:" + result.Marker
			return json.Marshal(result)
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/jobs/"+jobqueue.EncodeJobID(jobID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotKind != "adminapi_test.output" {
		t.Errorf("OutputDecryptor called with kind = %q, want %q", gotKind, "adminapi_test.output")
	}

	var env struct {
		Data jobDetailView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var result outputTestResult
	if err := json.Unmarshal(env.Data.Output, &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if result.Marker != "decrypted:encrypted-marker" {
		t.Errorf("Output.marker = %q, want %q (OutputDecryptor should have transformed it)", result.Marker, "decrypted:encrypted-marker")
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

	req := httptest.NewRequest(http.MethodPost, "/admin/jobs/"+jobqueue.EncodeJobID(jobID)+"/cancel", strings.NewReader(`{"reason":"test"}`))
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

	req := httptest.NewRequest(http.MethodPost, "/admin/jobs/"+jobqueue.EncodeJobID(jobID)+"/retry", nil)
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
