package systemworker

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"go.temporal.io/sdk/workflow"
)

func newTestTemporalClient(t *testing.T) *temporal.Client {
	t.Helper()
	t.Setenv("GOERP_TEMPORAL_HOST_PORT", "127.0.0.1:7233")
	t.Setenv("GOERP_TEMPORAL_NAMESPACE", "default")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := temporal.New(ctx)
	if err != nil {
		t.Skipf("temporal not reachable at 127.0.0.1:7233 (start compose.dev.yml): %v", err)
	}
	return c
}

// dummyWorkflow stands in for ProvisionTenantWorkflow/OffboardTenantWorkflow
// (goerp#149/#150), which don't exist yet — this test only exercises the
// worker plumbing, not any real workflow's behavior.
func dummyWorkflow(ctx workflow.Context) error { return nil }

func TestStartRegistersAndConfirmsPollers(t *testing.T) {
	temporalClient := newTestTemporalClient(t)
	defer temporalClient.Close()

	w := New(temporalClient)
	w.RegisterWorkflow(dummyWorkflow)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer w.Stop()

	has, err := temporalClient.HasPollers(context.Background(), TaskQueue)
	if err != nil {
		t.Fatalf("HasPollers() error: %v", err)
	}
	if !has {
		t.Error("HasPollers() = false after Start() succeeded, want true")
	}
}

// TestNilTemporalClientDoesNotPanic guards against a nil temporalClient
// (Temporal unreachable at Stage 1 — a legitimate, warn-only outcome
// engine.go's own construction already tolerates for this field) reaching
// New and then panicking on first use, rather than failing cleanly at
// Start.
func TestNilTemporalClientDoesNotPanic(t *testing.T) {
	w := New(nil)
	w.RegisterWorkflow(dummyWorkflow) // must not panic on a nil underlying worker
	w.Stop()                          // must not panic either

	if err := w.Start(context.Background()); err == nil {
		t.Error("Start() with a nil temporal client: expected an error, got nil")
	}
}

func TestStartWithNoRegistrationsStillSucceeds(t *testing.T) {
	temporalClient := newTestTemporalClient(t)
	defer temporalClient.Close()

	w := New(temporalClient)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start() with no registered workflows/activities: error = %v, want nil", err)
	}
	w.Stop()
}
