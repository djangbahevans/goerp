package jobqueue

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/riverqueue/river"
)

var idempotencyKeySeq atomic.Int64

// newIdempotencyKey returns a value unique to this test process, without
// pulling in a UUID dependency just for tests.
func newIdempotencyKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d-%d", t.Name(), time.Now().UnixNano(), idempotencyKeySeq.Add(1))
}

// testDSN mirrors README.md's documented dev-stack credentials
// (PgBouncer at localhost:6432, user/pass/db goerp/dev/goerp).
const testDSN = "postgres://goerp:dev@localhost:6432/goerp"

func testPool(t *testing.T) *sql.DB {
	t.Helper()

	pool, err := sql.Open("pgx", testDSN)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := pool.Ping(); err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", testDSN, err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	return pool
}

func testConfig() *config.Config {
	return &config.Config{
		QueueCriticalConcurrency: 1,
		QueueDefaultConcurrency:  1,
		QueueBulkConcurrency:     1,
		QueueSearchConcurrency:   1,
		QueueEmailConcurrency:    1,
	}
}

func startedClient(t *testing.T, workers *river.Workers) *river.Client[*sql.Tx] {
	t.Helper()
	pool := testPool(t)

	client, err := New(pool, testConfig(), workers)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
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

// waitForCompletion blocks until a job_completed event fires for jobID, or
// fails the test after timeout.
func waitForCompletion(t *testing.T, client *river.Client[*sql.Tx], jobID int64, timeout time.Duration) {
	t.Helper()

	sub, cancel := client.Subscribe(river.EventKindJobCompleted)
	defer cancel()

	deadline := time.After(timeout)
	for {
		select {
		case event := <-sub:
			if event.Job.ID == jobID {
				return
			}
		case <-deadline:
			t.Fatalf("job %d did not complete within %s", jobID, timeout)
		}
	}
}

func TestProbeJob_DispatchableEndToEnd(t *testing.T) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &ProbeWorker{})
	client := startedClient(t, workers)

	row, err := client.Insert(context.Background(), ProbeArgs{
		IdempotencyKey: newIdempotencyKey(t),
		Message:        "hello from a test",
	}, &river.InsertOpts{Queue: QueueDefault})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	waitForCompletion(t, client, row.Job.ID, 10*time.Second)
}

func TestNew_RoutesAllFiveQueues(t *testing.T) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &ProbeWorker{})
	client := startedClient(t, workers)

	ctx := context.Background()
	sub, cancel := client.Subscribe(river.EventKindJobCompleted)
	defer cancel()

	for _, queue := range []string{QueueCritical, QueueDefault, QueueBulk, QueueSearch, QueueEmail} {
		if _, err := client.Insert(ctx, ProbeArgs{
			IdempotencyKey: newIdempotencyKey(t),
			Message:        "queue routing check",
		}, &river.InsertOpts{Queue: queue}); err != nil {
			t.Fatalf("Insert into queue %q: %v", queue, err)
		}
	}

	seen := make(map[string]bool, 5)
	deadline := time.After(10 * time.Second)
	for len(seen) < 5 {
		select {
		case event := <-sub:
			seen[event.Job.Queue] = true
		case <-deadline:
			t.Fatalf("only saw completions from queues %v, want all 5", seen)
		}
	}
}

func TestProbeJob_DuplicateIdempotencyKeyIsNoOp(t *testing.T) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &ProbeWorker{})
	client := startedClient(t, workers)

	key := newIdempotencyKey(t)
	args := ProbeArgs{IdempotencyKey: key, Message: "first"}

	first, err := client.Insert(context.Background(), args, &river.InsertOpts{Queue: QueueBulk})
	if err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	if first.UniqueSkippedAsDuplicate {
		t.Fatal("first insert unexpectedly reported as a duplicate")
	}

	second, err := client.Insert(context.Background(), ProbeArgs{IdempotencyKey: key, Message: "second"}, &river.InsertOpts{Queue: QueueBulk})
	if err != nil {
		t.Fatalf("second Insert: %v", err)
	}
	if !second.UniqueSkippedAsDuplicate {
		t.Fatal("second insert with the same idempotency key was not skipped as a duplicate")
	}
	if second.Job.ID != first.Job.ID {
		t.Fatalf("second insert's job ID = %d, want the same as the first (%d) — a no-op, not a new row", second.Job.ID, first.Job.ID)
	}

	waitForCompletion(t, client, first.Job.ID, 10*time.Second)
}

// concurrencyWorker blocks until release is closed, and records how many
// concurrent Work calls were in flight at once.
type concurrencyWorker struct {
	river.WorkerDefaults[ProbeArgs]
	inFlight atomic.Int32
	maxSeen  atomic.Int32
	release  chan struct{}
}

func (w *concurrencyWorker) Work(ctx context.Context, job *river.Job[ProbeArgs]) error {
	n := w.inFlight.Add(1)
	defer w.inFlight.Add(-1)

	for {
		old := w.maxSeen.Load()
		if n <= old || w.maxSeen.CompareAndSwap(old, n) {
			break
		}
	}

	<-w.release
	return nil
}

func TestQueue_EnforcesPerQueueConcurrencyLimit(t *testing.T) {
	release := make(chan struct{})
	worker := &concurrencyWorker{release: release}
	workers := river.NewWorkers()
	river.AddWorker(workers, worker)

	pool := testPool(t)
	cfg := testConfig()
	cfg.QueueBulkConcurrency = 2 // deliberately small and distinct from the job count below

	client, err := New(pool, cfg, workers)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	}()

	const jobCount = 5
	sub, cancelSub := client.Subscribe(river.EventKindJobCompleted)
	defer cancelSub()

	for i := range jobCount {
		if _, err := client.Insert(ctx, ProbeArgs{
			IdempotencyKey: newIdempotencyKey(t),
			Message:        fmt.Sprintf("job %d", i),
		}, &river.InsertOpts{Queue: QueueBulk}); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	// Give the client time to fetch and start as many jobs as its
	// concurrency limit allows before releasing them.
	time.Sleep(500 * time.Millisecond)
	close(release)

	completed := 0
	deadline := time.After(10 * time.Second)
	for completed < jobCount {
		select {
		case <-sub:
			completed++
		case <-deadline:
			t.Fatalf("only %d/%d jobs completed within timeout", completed, jobCount)
		}
	}

	if got := worker.maxSeen.Load(); got > int32(cfg.QueueBulkConcurrency) {
		t.Errorf("max concurrent Work() calls = %d, want <= %d (queue concurrency limit)", got, cfg.QueueBulkConcurrency)
	}
}
