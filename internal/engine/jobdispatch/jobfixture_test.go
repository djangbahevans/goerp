package jobdispatch

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

const jobFixtureModuleName = "jobfixture"

// jobFixtureWorkPayload and jobFixtureObserved mirror testdata/jobfixture's
// own workPayload and observed wire types.
type jobFixtureWorkPayload struct {
	Note string `msgpack:"note"`
	Mode string `msgpack:"mode"`
}

type jobFixtureObserved struct {
	JobID       string `msgpack:"job_id"`
	JobType     string `msgpack:"job_type"`
	TenantID    string `msgpack:"tenant_id"`
	TraceID     string `msgpack:"trace_id"`
	Attempt     int    `msgpack:"attempt"`
	MaxAttempts int    `msgpack:"max_attempts"`
	Note        string `msgpack:"note"`
}

// jobFixtureEnv is a real Worker for testdata/jobfixture, the
// rivertest.Worker driving it through River's real job executor, and the
// transaction every job it works runs in.
type jobFixtureEnv struct {
	tenantID string
	jobsConn *sql.DB
	tester   *rivertest.Worker[jobqueue.WASMJobArgs, pgx.Tx]
	client   *river.Client[pgx.Tx]
	tx       pgx.Tx
}

// newJobFixtureEnv compiles testdata/jobfixture and loads it as a
// StatusReady module declaring its three job types and jobs.enqueue, on a
// wasm.Runtime whose primary DB is the job queue's, so the fixture's own
// jobs.Enqueue calls land in the same river_job table the test reads.
func newJobFixtureEnv(t *testing.T) *jobFixtureEnv {
	t.Helper()
	ctx := t.Context()

	jobsConn := openJobsConn(t)
	conn, tenantStore := newTestTenantStore(t)
	tt := newFixtureTenant(t, conn, tenantStore)
	cleanupRiverJobsForTenant(t, jobsConn, tt.ID)

	rt := newTestWasmRuntimeWithPrimaryDB(t, jobsConn)
	compiled, err := rt.CompileModule(ctx, compileFixture(t, "jobfixture"))
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	pool := rt.NewPool(jobFixtureModuleName, compiled, wasm.PoolConfig{MaxSize: 2, BorrowTimeout: 5 * time.Second})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 5*time.Second) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		jobFixtureModuleName: {
			Status: module.StatusReady,
			Pool:   pool,
			Manifest: manifest.Manifest{
				Name: jobFixtureModuleName,
				Type: "standard",
				JobTypes: []manifest.JobType{
					{Name: "jobfixture_start", Handler: "jobfixture_start", Queue: jobqueue.QueueDefault},
					{Name: "jobfixture_work", Handler: "jobfixture_work", Queue: jobqueue.QueueDefault},
					{Name: "jobfixture_observed", Handler: "jobfixture_observed", Queue: jobqueue.QueueDefault},
				},
			},
			Capabilities: abi.CapJobsEnqueue,
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}
	w := &Worker{ModuleRegistry: reg, Runtime: rt, TenantStore: tenantStore}

	pgxPool, err := pgxpool.New(ctx, jobsTestDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pgxPool.Close)
	tx, err := pgxPool.Begin(ctx)
	if err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", jobsTestDSN, err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	driver := riverpgxv5.New(pgxPool)
	client, err := river.NewClient(driver, &river.Config{Schema: jobqueue.Schema})
	if err != nil {
		t.Fatalf("river.NewClient: %v", err)
	}

	return &jobFixtureEnv{
		tenantID: tt.ID,
		jobsConn: jobsConn,
		tester:   rivertest.NewWorker(t, driver, &river.Config{Schema: jobqueue.Schema}, river.Worker[jobqueue.WASMJobArgs](w)),
		client:   client,
		tx:       tx,
	}
}

// insertAndWork inserts a jobType job carrying payload in e.tx and works
// it through Worker.Work. The returned error is Work's own, nil for a job
// that succeeded or was cancelled.
func (e *jobFixtureEnv) insertAndWork(t *testing.T, jobType string, payload jobFixtureWorkPayload, maxAttempts int, traceID string) (*rivertest.WorkResult, error) {
	t.Helper()
	data, err := msgpack.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return e.tester.Work(t.Context(), t, e.tx, jobqueue.WASMJobArgs{
		ModuleName: jobFixtureModuleName, JobType: jobType, TenantID: e.tenantID,
		Payload: data, MaxAttempts: maxAttempts, TraceID: traceID,
	}, nil)
}

// work works an already-inserted job again, returning Work's own error.
func (e *jobFixtureEnv) work(t *testing.T, job *rivertype.JobRow) (*rivertest.WorkResult, error) {
	t.Helper()
	return e.tester.WorkJob(t.Context(), t, e.tx, job)
}

// enqueuedJobs returns the jobType rows the fixture itself enqueued
// through jobs.Enqueue, oldest first.
func (e *jobFixtureEnv) enqueuedJobs(t *testing.T, jobType string) []*rivertype.JobRow {
	t.Helper()
	rows, err := e.jobsConn.Query(
		`SELECT id FROM system.river_job WHERE kind = 'wasm_job' AND args->>'job_type' = $1 AND args->>'tenant_id' = $2 ORDER BY id`,
		jobType, e.tenantID,
	)
	if err != nil {
		t.Fatalf("query enqueued %s jobs: %v", jobType, err)
	}
	defer rows.Close()

	var jobs []*rivertype.JobRow
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan job id: %v", err)
		}
		job, err := e.client.JobGetTx(t.Context(), e.tx, id)
		if err != nil {
			t.Fatalf("JobGetTx(%d): %v", id, err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate enqueued %s jobs: %v", jobType, err)
	}
	return jobs
}

// observations decodes every jobfixture_observed job's payload, oldest
// first — one per jobfixture_work attempt.
func (e *jobFixtureEnv) observations(t *testing.T) []jobFixtureObserved {
	t.Helper()
	var out []jobFixtureObserved
	for _, job := range e.enqueuedJobs(t, "jobfixture_observed") {
		var args jobqueue.WASMJobArgs
		if err := json.Unmarshal(job.EncodedArgs, &args); err != nil {
			t.Fatalf("unmarshal args: %v", err)
		}
		var obs jobFixtureObserved
		if err := msgpack.Unmarshal(args.Payload, &obs); err != nil {
			t.Fatalf("decode observed payload: %v", err)
		}
		out = append(out, obs)
	}
	return out
}

// TestWork_RealCompiledJobFixture_EnqueuedJobRunsThroughOnJob is the end
// to end path for an ordinary job: a real module's jobfixture_start
// handler enqueues a jobfixture_work job with jobs.Enqueue, and
// Worker.Work runs that job through handle_job, engine.DispatchJob and the
// jobfixture_work handler registered with engine.OnJob, which sees the
// job's real River metadata and the decoded payload.
func TestWork_RealCompiledJobFixture_EnqueuedJobRunsThroughOnJob(t *testing.T) {
	e := newJobFixtureEnv(t)

	start, err := e.insertAndWork(t, "jobfixture_start", jobFixtureWorkPayload{Note: "hello"}, 0, "trace-jobfixture")
	if err != nil {
		t.Fatalf("work jobfixture_start: %v", err)
	}
	if start.Job.State != rivertype.JobStateCompleted {
		t.Fatalf("jobfixture_start state = %s, want completed", start.Job.State)
	}

	enqueued := e.enqueuedJobs(t, "jobfixture_work")
	if len(enqueued) != 1 {
		t.Fatalf("jobfixture_start enqueued %d jobfixture_work jobs, want 1", len(enqueued))
	}
	work, err := e.work(t, enqueued[0])
	if err != nil {
		t.Fatalf("work jobfixture_work: %v", err)
	}
	if work.Job.State != rivertype.JobStateCompleted {
		t.Fatalf("jobfixture_work state = %s, want completed", work.Job.State)
	}

	obs := e.observations(t)
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	want := jobFixtureObserved{
		JobID: jobqueue.EncodeJobID(work.Job.ID), JobType: "jobfixture_work", TenantID: e.tenantID,
		TraceID: "trace-jobfixture", Attempt: 1, MaxAttempts: 4, Note: "hello",
	}
	if obs[0] != want {
		t.Errorf("observed JobContext = %+v, want %+v", obs[0], want)
	}
}

// TestWork_RealCompiledJobFixture_PlainErrorRetriesUntilMaxAttempts: a
// handler's plain error leaves the job retryable until its last attempt,
// which discards it.
func TestWork_RealCompiledJobFixture_PlainErrorRetriesUntilMaxAttempts(t *testing.T) {
	e := newJobFixtureEnv(t)

	res, err := e.insertAndWork(t, "jobfixture_work", jobFixtureWorkPayload{Mode: "fail"}, 2, "")
	if err == nil {
		t.Fatal("Work() error = nil on attempt 1, want the handler's failure")
	}
	// River schedules a short first backoff straight back to available.
	if res.Job.State != rivertype.JobStateRetryable && res.Job.State != rivertype.JobStateAvailable {
		t.Fatalf("state after attempt 1 = %s, want retryable or available", res.Job.State)
	}

	res, err = e.work(t, res.Job)
	if err == nil {
		t.Fatal("Work() error = nil on attempt 2, want the handler's failure")
	}
	if res.Job.State != rivertype.JobStateDiscarded {
		t.Fatalf("state after attempt 2 of 2 = %s, want discarded", res.Job.State)
	}

	obs := e.observations(t)
	if len(obs) != 2 || obs[0].Attempt != 1 || obs[1].Attempt != 2 || obs[1].MaxAttempts != 2 {
		t.Errorf("observations = %+v, want attempts 1 and 2 of 2", obs)
	}
}

// TestWork_RealCompiledJobFixture_PermanentErrorCancelsImmediately: a
// handler returning jobs.PermanentError cancels the job on its first
// attempt, with attempts to spare.
func TestWork_RealCompiledJobFixture_PermanentErrorCancelsImmediately(t *testing.T) {
	e := newJobFixtureEnv(t)

	res, err := e.insertAndWork(t, "jobfixture_work", jobFixtureWorkPayload{Mode: "permanent"}, 5, "")
	if err != nil {
		t.Fatalf("Work() error = %v, want nil: River reports a cancel through the job state", err)
	}
	if res.Job.State != rivertype.JobStateCancelled {
		t.Fatalf("state = %s, want cancelled", res.Job.State)
	}
	if res.Job.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", res.Job.Attempt)
	}
}
