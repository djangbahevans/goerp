package jobqueue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
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

// TestMigrate_ConcurrentCallersDoNotRace guards against the race behind
// this exact failure showing up in CI: two different packages' tests
// (this one and internal/engine/adminapi's) both call Migrate against the
// same real dev Postgres instance, and go test ./... runs different
// packages' test binaries concurrently by default. Without Migrate's own
// advisory lock, River's migration DDL (CREATE TYPE/CREATE FUNCTION, no
// IF NOT EXISTS guard) collided on Postgres's pg_type/pg_proc catalogs.
// This test can't reproduce the cross-package race directly, but it does
// confirm concurrent same-process callers of Migrate now serialize
// cleanly instead of erroring.
func TestMigrate_ConcurrentCallersDoNotRace(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", testDSN, err)
	}
	t.Cleanup(pool.Close)

	const callers = 5
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			errs <- Migrate(ctx, pool)
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Migrate() error: %v", err)
		}
	}
}

// TestMigrate_SingleConnectionPoolDoesNotDeadlock guards against Migrate
// holding its advisory lock on a connection reserved from pool via
// Acquire, rather than one opened independently: reserving a pool
// connection to hold the lock would leave the migration itself (through
// riverpgxv5.New(pool)) competing with that reservation for the pool's own
// connections — on a pool with no spare capacity, the migration would
// never get one, deadlocking until the caller's own timeout kills it.
// MaxConns: 1 here reproduces that exact starvation scenario in
// miniature; a bounded context makes the test fail fast instead of
// hanging for real if this regresses.
func TestMigrate_SingleConnectionPoolDoesNotDeadlock(t *testing.T) {
	cfg, err := pgxpool.ParseConfig(testDSN)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.MaxConns = 1

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", testDSN, err)
	}
	t.Cleanup(pool.Close)

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate() with a single-connection pool: %v", err)
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", testDSN, err)
	}
	t.Cleanup(pool.Close)

	if err := Migrate(ctx, pool); err != nil {
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

func startedClient(t *testing.T, workers *river.Workers) *river.Client[pgx.Tx] {
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

// waitForCompletion polls JobGet until jobID reaches JobStateCompleted, or
// fails the test after timeout. Polling the shared job row (rather than
// Subscribe, which only delivers events for jobs *this* client instance
// itself worked) is correct even when another client — a concurrently
// running test in another package, hitting the same real Postgres — races
// in and completes the job first.
func waitForCompletion(t *testing.T, client *river.Client[pgx.Tx], jobID int64, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		job, err := client.JobGet(context.Background(), jobID)
		if err != nil {
			t.Fatalf("JobGet(%d): %v", jobID, err)
		}
		if job.State == rivertype.JobStateCompleted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d did not complete within %s (state: %s)", jobID, timeout, job.State)
		}
		time.Sleep(20 * time.Millisecond)
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
	for _, queue := range []string{QueueCritical, QueueDefault, QueueBulk, QueueSearch, QueueEmail} {
		row, err := client.Insert(ctx, ProbeArgs{
			IdempotencyKey: newIdempotencyKey(t),
			Message:        "queue routing check",
		}, &river.InsertOpts{Queue: queue})
		if err != nil {
			t.Fatalf("Insert into queue %q: %v", queue, err)
		}
		waitForCompletion(t, client, row.Job.ID, 10*time.Second)
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
	jobIDs := make([]int64, jobCount)
	for i := range jobCount {
		row, err := client.Insert(ctx, ProbeArgs{
			IdempotencyKey: newIdempotencyKey(t),
			Message:        fmt.Sprintf("job %d", i),
		}, &river.InsertOpts{Queue: QueueBulk})
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
		jobIDs[i] = row.Job.ID
	}

	// Give the client time to fetch and start as many jobs as its
	// concurrency limit allows before releasing them.
	time.Sleep(500 * time.Millisecond)
	close(release)

	for _, id := range jobIDs {
		waitForCompletion(t, client, id, 10*time.Second)
	}

	if got := worker.maxSeen.Load(); got > int32(cfg.QueueBulkConcurrency) {
		t.Errorf("max concurrent Work() calls = %d, want <= %d (queue concurrency limit)", got, cfg.QueueBulkConcurrency)
	}
}
