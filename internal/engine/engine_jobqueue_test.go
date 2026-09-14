package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func TestStart_JobQueueWorkerProcessesJobs(t *testing.T) {
	cfg := baseTestConfig(t)

	e, err := New(cfg)
	skipIfInfraUnreachable(t, err)
	t.Cleanup(func() { _ = e.primaryDB.Close() })

	// 20s, not the 5s most other Start()-timeout tests in this package use
	// (engine_test.go, ratelimit_test.go) — this is the one test whose
	// Start() path also waits on a real Temporal WaitForPollers round trip
	// (systemworker.Worker.Start), which has been observed timing out under
	// CI resource contention (many packages' test binaries hitting the same
	// shared Postgres/Temporal instances concurrently) well short of a hang.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(func() {
		if err := e.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error: %v", err)
		}
	})

	client := e.JobQueue()
	row, err := client.Insert(ctx, jobqueue.ProbeArgs{
		IdempotencyKey: fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()),
		Message:        "engine startup smoke test",
	}, &river.InsertOpts{Queue: jobqueue.QueueDefault})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Polling JobGet (the shared job row) rather than Subscribe (which
	// only delivers events this client instance itself worked) stays
	// correct even if another concurrently running test's client races in
	// and processes the job first.
	deadline := time.Now().Add(20 * time.Second)
	for {
		job, err := client.JobGet(context.Background(), row.Job.ID)
		if err != nil {
			t.Fatalf("JobGet: %v", err)
		}
		if job.State == rivertype.JobStateCompleted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe job %d did not complete within timeout (state: %s)", row.Job.ID, job.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
