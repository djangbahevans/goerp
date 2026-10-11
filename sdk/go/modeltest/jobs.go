package modeltest

import (
	"bytes"
	"database/sql"
	"encoding/json/v2"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/vmihailenco/msgpack/v5"
)

// Job describes a committed enqueue observed by h.Jobs. Payload contains object
// payload fields; RawPayload retains the MessagePack bytes for every payload shape.
type Job struct {
	ID          string
	Type        string
	Payload     map[string]any
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
}

func newTestJobs(t failer, db *sql.DB, tenantID, moduleName string) *TestJobs {
	return &TestJobs{t: t, db: db, tenantID: tenantID, moduleName: moduleName}
}

func (j *TestJobs) matching(def jobs.Definition) []Job {
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
	}
	defer func() { _ = rows.Close() }()

	var result []Job
	for rows.Next() {
		var item Job
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw, &item.Queue, &item.MaxAttempts, &item.ScheduledAt); err != nil {
			j.t.Fatalf("modeltest: read queued job: %v", err)
		}
		var args jobqueue.WASMJobArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			j.t.Fatalf("modeltest: decode queued job: %v", err)
		}
		if len(args.Payload) > 0 {
			var decoded any
			decoder := msgpack.NewDecoder(bytes.NewReader(args.Payload))
			decoder.UseLooseInterfaceDecoding(true)
			if err := decoder.Decode(&decoded); err != nil {
				j.t.Fatalf("modeltest: decode queued job payload: %v", err)
			}
			item.Payload, _ = decoded.(map[string]any)
		}
		item.ID = jobqueue.EncodeJobID(id)
		item.Type = args.JobType
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
	j.AssertEnqueuedCount(def, 0)
}

// AssertEnqueuedCount fails the test unless def has exactly count committed enqueues.
func (j *TestJobs) AssertEnqueuedCount(def jobs.Definition, count int) {
	j.t.Helper()
	if got := len(j.matching(def)); got != count {
		j.t.Fatalf("modeltest: job %q enqueued %d times, want %d", def.Name(), got, count)
	}
}

// Last returns def's most recent committed enqueue, failing the test when none exists.
func (j *TestJobs) Last(def jobs.Definition) *Job {
	j.t.Helper()
	items := j.matching(def)
	if len(items) == 0 {
		j.t.Fatalf("modeltest: expected job %q to be enqueued", def.Name())
		return nil
	}
	return &items[len(items)-1]
}
