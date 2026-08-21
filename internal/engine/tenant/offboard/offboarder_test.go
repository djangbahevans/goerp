package tenantoffboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// TestStartOffboard_NilTemporalClientDoesNotPanic guards against
// Engine.New's temporalClient field being nil (Temporal unreachable at
// startup, warn-only) reaching Offboarder and panicking on first use
// instead of failing cleanly — same regression tenantprovision.Provisioner
// already guards against.
func TestStartOffboard_NilTemporalClientDoesNotPanic(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	o := NewOffboarder(env.tenantStore, nil, "goerp-system", nil, jobqueue.QueueAdmin)

	_, err := o.StartOffboard(context.Background(), slug, 30*24*time.Hour, false)
	if err == nil {
		t.Error("StartOffboard() with a nil temporal client: expected an error, got nil")
	}
}

func TestStartOffboard_GracePeriodPathReturnsScheduled(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	o := NewOffboarder(env.tenantStore, env.temporalClient, env.taskQueue, nil, jobqueue.QueueAdmin)

	before := time.Now()
	result, err := o.StartOffboard(context.Background(), slug, time.Hour, false)
	if err != nil {
		t.Fatalf("StartOffboard() error: %v", err)
	}
	if result.Status != "scheduled" {
		t.Errorf("Status = %q, want %q", result.Status, "scheduled")
	}
	if result.DeleteAt == nil {
		t.Fatal("expected DeleteAt to be set")
	}
	if result.DeleteAt.Before(before.Add(time.Hour)) || result.DeleteAt.After(time.Now().Add(time.Hour)) {
		t.Errorf("DeleteAt = %v, want roughly now+1h", result.DeleteAt)
	}

	// StartOffboard only starts the workflow — MarkOffboarding is its
	// first activity, run asynchronously after ExecuteWorkflow returns.
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := env.tenantStore.GetBySlug(context.Background(), slug)
		if err != nil {
			t.Fatalf("GetBySlug() error: %v", err)
		}
		if got.Status == tenant.StatusOffboarding {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tenant never reached StatusOffboarding (last status: %q)", got.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

const jobsTestDSN = "postgres://goerp:dev@localhost:6432/goerp"

// newTestJobClient builds a river.Client directly against the real dev
// Postgres, rather than through jobqueue.New — jobqueue.New always
// registers the fixed jobqueue.QueueAdmin queue name, and every
// concurrently running test package's own river.Client (built the same
// way) would then also poll that literal "admin" queue in the shared dev
// jobs table, picking up and mis-handling ("Unhandled job kind") a job
// this test inserted before this test's own, correctly configured client
// gets a chance to. queueName is a per-test-unique string for exactly the
// reason internal/engine/tenantprovision's and this package's own
// Temporal tests use a per-test-unique taskQueue: no other test process
// is polling it, so there's nothing to race against.
func newTestJobClient(t *testing.T, queueName string, activities *Activities, tenantStore *tenant.Store) *river.Client[pgx.Tx] {
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

	workers := river.NewWorkers()
	river.AddWorker(workers, &ImmediateWorker{Activities: activities, TenantStore: tenantStore})

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{queueName: {MaxWorkers: 2}},
		Workers: workers,
	})
	if err != nil {
		t.Fatalf("river.NewClient: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	})

	return client
}

func TestStartOffboard_ImmediatePathDeletesTenant(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	queueName := "test-tenantoffboard-admin-" + uniqueSlug(t)
	jobClient := newTestJobClient(t, queueName, env.activities, env.tenantStore)
	o := NewOffboarder(env.tenantStore, env.temporalClient, env.taskQueue, jobClient, queueName)

	result, err := o.StartOffboard(context.Background(), slug, 0, true)
	if err != nil {
		t.Fatalf("StartOffboard() error: %v", err)
	}
	if result.Status != "accepted" {
		t.Errorf("Status = %q, want %q", result.Status, "accepted")
	}
	if result.JobID == "" {
		t.Error("expected a non-empty JobID")
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := env.tenantStore.GetBySlug(context.Background(), slug)
		if err != nil {
			t.Fatalf("GetBySlug() error: %v", err)
		}
		if got.Status == tenant.StatusDeleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tenant not deleted after 10s (last status: %q)", got.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if schemaExists(t, env.conn, slug) {
		t.Error("expected tenant schema to have been dropped by the immediate job")
	}
}

func TestCancelOffboard_NotOffboardingReturnsError(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	o := NewOffboarder(env.tenantStore, env.temporalClient, env.taskQueue, nil, jobqueue.QueueAdmin)

	if err := o.CancelOffboard(context.Background(), slug); !errors.Is(err, tenant.ErrOffboardNotCancellable) {
		t.Errorf("CancelOffboard() on an active tenant: error = %v, want ErrOffboardNotCancellable", err)
	}
}
