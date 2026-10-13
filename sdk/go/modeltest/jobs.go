package modeltest

import (
	"bytes"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/vmihailenco/msgpack/v5"
)

// Job describes a committed enqueue with its payload decoded into P.
// RawPayload retains the original MessagePack bytes.
type Job[P any] struct {
	ID          string
	Type        string
	Payload     P
	RawPayload  []byte
	Queue       string
	MaxAttempts int
	ScheduledAt time.Time
}

// TestJobs asserts against committed jobs belonging to the harness tenant and module.
// Assertions do not run workers or change a queued job's state.
type TestJobs struct {
	t          failer
	db         *sql.DB
	tenantID   string
	moduleName string
	mu         *sync.Mutex
	runs       map[string]JobRun
}

func newTestJobs(t failer, db *sql.DB, tenantID, moduleName string) *TestJobs {
	return &TestJobs{
		t:          t,
		db:         db,
		tenantID:   tenantID,
		moduleName: moduleName,
		mu:         new(sync.Mutex),
		runs:       make(map[string]JobRun),
	}
}

func (j *TestJobs) matching(def jobs.Definition) []Job[[]byte] {
	j.t.Helper()

	if def.Name() == "" {
		j.t.Fatalf("modeltest: job definition must not be empty")
		return nil
	}

	rows, err := j.db.Query(`SELECT id, args, queue, max_attempts, scheduled_at FROM system.river_job
		WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1 AND args->>'module_name' = $2
		AND args->>'job_type' = $3 ORDER BY id`, j.tenantID, j.moduleName, def.Name())
	if err != nil {
		j.t.Fatalf("modeltest: query queued jobs: %v", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var result []Job[[]byte]
	for rows.Next() {
		var item Job[[]byte]
		var id int64
		var raw []byte

		if err := rows.Scan(&id, &raw, &item.Queue, &item.MaxAttempts, &item.ScheduledAt); err != nil {
			j.t.Fatalf("modeltest: read queued job: %v", err)
			return nil
		}

		var args jobqueue.WASMJobArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			j.t.Fatalf("modeltest: decode queued job: %v", err)
			return nil
		}

		item.ID = jobqueue.EncodeJobID(id)
		item.Type = args.JobType
		item.Payload = args.Payload
		item.RawPayload = args.Payload
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		j.t.Fatalf("modeltest: iterate queued jobs: %v", err)
	}

	return result
}

// AssertEnqueued fails the test unless def has at least one committed enqueue.
func (j *TestJobs) AssertEnqueued(def jobs.Definition) {
	j.t.Helper()

	if len(j.matching(def)) == 0 {
		j.t.Fatalf("modeltest: expected job %q to be enqueued", def.Name())
	}
}

// AssertNotEnqueued fails the test if def has a committed enqueue.
func (j *TestJobs) AssertNotEnqueued(def jobs.Definition) {
	j.t.Helper()

	j.AssertEnqueuedCount(def, 0)
}

// AssertEnqueuedCount fails the test unless def has exactly count committed enqueues.
func (j *TestJobs) AssertEnqueuedCount(def jobs.Definition, count int) {
	j.t.Helper()

	if got := len(j.matching(def)); got != count {
		j.t.Fatalf("modeltest: job %q enqueued %d times, want %d", def.Name(), got, count)
	}
}

// Last returns def's most recent committed enqueue with its payload decoded into P.
// It fails the test if no matching job exists or the payload cannot decode.
func (j *TestJobs) Last[P any](def jobs.Def[P]) *Job[P] {
	j.t.Helper()

	items := j.matching(def)
	if len(items) == 0 {
		j.t.Fatalf("modeltest: expected job %q to be enqueued", def.Name())
		return nil
	}
	last := items[len(items)-1]

	var payload P
	if err := msgpack.Unmarshal(last.RawPayload, &payload); err != nil {
		j.t.Fatalf("modeltest: decode job %q payload: %v", def.Name(), err)
		return nil
	}

	return &Job[P]{
		ID:          last.ID,
		Type:        last.Type,
		Payload:     payload,
		RawPayload:  last.RawPayload,
		Queue:       last.Queue,
		MaxAttempts: last.MaxAttempts,
		ScheduledAt: last.ScheduledAt,
	}
}

// JobRun contains the latest RunJob execution's report. Result is absent after a
// failed execution and when the handler did not set one.
type JobRun struct {
	Percent int
	Message string
	Result  jsontext.Value
}

// DecodeResult decodes a successful execution's JSON result into R.
func (r *JobRun) DecodeResult[R any]() (R, error) {
	var result R
	err := json.Unmarshal(r.Result, &result)
	return result, err
}

// LastRun returns the latest RunJob report for def and fails if it has not run.
func (j *TestJobs) LastRun(def jobs.Definition) *JobRun {
	j.t.Helper()

	j.mu.Lock()
	defer j.mu.Unlock()

	report, ok := j.runs[def.Name()]
	if !ok {
		j.t.Fatalf("modeltest: job %q has not run", def.Name())
		return nil
	}

	report.Result = bytes.Clone(report.Result)
	return &report
}
