// Package systemworker runs engine-owned Temporal workflows and activities in process on a
// dedicated task queue. Worker startup failures propagate to Engine.Start.
package systemworker

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"go.temporal.io/sdk/worker"
)

// TaskQueue is deliberately not shaped like a module's "goerp:{module_name}"
// task queue (workflow-guide.md §2) — no module name could ever collide
// with it.
const TaskQueue = "goerp-system"

type Worker struct {
	w        worker.Worker
	temporal *temporal.Client
}

// New never dereferences temporalClient — it's warn-only constructed at
// Stage 1 (Engine.New) and can legitimately be nil (Temporal unreachable
// at startup), the same as workflowworker.NewManager's own temporalClient
// parameter. s.w stays nil in that case; RegisterWorkflow/RegisterActivity
// become safe no-ops and Start fails clearly instead of panicking on a
// nil worker.Worker.
func New(temporalClient *temporal.Client) *Worker {
	w := &Worker{temporal: temporalClient}
	if temporalClient != nil {
		w.w = temporalClient.NewWorker(TaskQueue, worker.Options{})
	}
	return w
}

// RegisterWorkflow and RegisterActivity must be called before Start.
func (s *Worker) RegisterWorkflow(fn any) {
	if s.w != nil {
		s.w.RegisterWorkflow(fn)
	}
}

func (s *Worker) RegisterActivity(fn any) {
	if s.w != nil {
		s.w.RegisterActivity(fn)
	}
}

// Start polls TaskQueue and confirms worker registration before returning.
func (s *Worker) Start(ctx context.Context) error {
	if s.w == nil {
		return fmt.Errorf("temporal client unavailable")
	}
	if err := s.w.Start(); err != nil {
		return fmt.Errorf("start temporal worker: %w", err)
	}
	if err := s.temporal.WaitForPollers(ctx, TaskQueue); err != nil {
		s.w.Stop()
		return fmt.Errorf("confirm system worker registration: %w", err)
	}
	return nil
}

func (s *Worker) Stop() {
	if s.w != nil {
		s.w.Stop()
	}
}
