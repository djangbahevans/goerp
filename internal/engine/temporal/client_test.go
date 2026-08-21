package temporal

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// localConfig points at the compose.dev.yml Temporal instance
// (127.0.0.1:7233, "default" namespace — 127.0.0.1 rather than
// "localhost" since gRPC's dialer can stall for several seconds trying
// an unreachable ::1 first in IPv6-loopback-but-unrouted environments).
// Tests using it are skipped if it isn't reachable, so `go test ./...`
// still passes without Docker running.
func localConfig() Config {
	return Config{HostPort: "127.0.0.1:7233", Namespace: "default"}
}

func skipIfUnreachable(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Skipf("temporal not reachable at 127.0.0.1:7233 (start compose.dev.yml): %v", err)
	}
}

func newTestClient(t *testing.T, ctx context.Context) *Client {
	t.Helper()
	t.Setenv("GOERP_TEMPORAL_HOST_PORT", localConfig().HostPort)
	t.Setenv("GOERP_TEMPORAL_NAMESPACE", localConfig().Namespace)

	c, err := New(ctx)
	skipIfUnreachable(t, err)
	return c
}

func TestNewConnectsAndPings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := newTestClient(t, ctx)
	defer c.Close()

	if err := c.Ping(ctx); err != nil {
		t.Errorf("Ping() error: %v", err)
	}
}

func TestNewInvalidAddr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	t.Setenv("GOERP_TEMPORAL_HOST_PORT", "127.0.0.1:1")

	_, err := New(ctx)
	if err == nil {
		t.Fatal("New() with an unreachable address: expected an error, got nil")
	}
}

func TestHasPollersNoPollers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := newTestClient(t, ctx)
	defer c.Close()

	has, err := c.HasPollers(ctx, "goerp:no-such-queue")
	if err != nil {
		t.Fatalf("HasPollers() error: %v", err)
	}
	if has {
		t.Error("HasPollers() = true for a task queue with no workers, want false")
	}
}

func TestNewWorkerAndWaitForPollers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := newTestClient(t, ctx)
	defer c.Close()

	const taskQueue = "test-newworker-queue"
	w := c.NewWorker(taskQueue, worker.Options{})
	if err := w.Start(); err != nil {
		t.Fatalf("worker.Start() error: %v", err)
	}
	defer w.Stop()

	if err := c.WaitForPollers(ctx, taskQueue); err != nil {
		t.Errorf("WaitForPollers() error: %v", err)
	}
}

func TestWaitForPollersTimesOutWithNoWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c := newTestClient(t, ctx)
	defer c.Close()

	if err := c.WaitForPollers(ctx, "goerp:no-such-queue"); err == nil {
		t.Error("WaitForPollers() with no worker registered: expected an error, got nil")
	}
}

func echoWorkflow(ctx workflow.Context, in string) (string, error) {
	return in, nil
}

func TestExecuteWorkflowRunsToCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := newTestClient(t, ctx)
	defer c.Close()

	const taskQueue = "test-executeworkflow-queue"
	w := c.NewWorker(taskQueue, worker.Options{})
	w.RegisterWorkflow(echoWorkflow)
	if err := w.Start(); err != nil {
		t.Fatalf("worker.Start() error: %v", err)
	}
	defer w.Stop()
	if err := c.WaitForPollers(ctx, taskQueue); err != nil {
		t.Fatalf("WaitForPollers() error: %v", err)
	}

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, echoWorkflow, "hello")
	if err != nil {
		t.Fatalf("ExecuteWorkflow() error: %v", err)
	}

	var out string
	if err := run.Get(ctx, &out); err != nil {
		t.Fatalf("run.Get() error: %v", err)
	}
	if out != "hello" {
		t.Errorf("workflow result = %q, want %q", out, "hello")
	}
}
