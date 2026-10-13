package jobqueue

import (
	"context"

	"github.com/djangbahevans/goerp/internal/engine/jobprogress"
	"github.com/riverqueue/river"
)

type JobProgressCleanupArgs struct{}

func (JobProgressCleanupArgs) Kind() string { return "job_progress_cleanup" }

func (JobProgressCleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}

type JobProgressCleanupWorker struct {
	river.WorkerDefaults[JobProgressCleanupArgs]
	Store *jobprogress.Store
}

func (w *JobProgressCleanupWorker) Work(ctx context.Context, _ *river.Job[JobProgressCleanupArgs]) error {
	return w.Store.Prune(ctx)
}
