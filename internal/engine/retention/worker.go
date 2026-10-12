package retention

import (
	"context"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

type Worker struct {
	river.WorkerDefaults[jobqueue.RetentionSweepArgs]
	Sweeper *Sweeper
}

func (*Worker) Timeout(*river.Job[jobqueue.RetentionSweepArgs]) time.Duration {
	return 30 * time.Minute
}

func (w *Worker) Work(ctx context.Context, job *river.Job[jobqueue.RetentionSweepArgs]) error {
	result, err := w.Sweeper.Run(ctx, job.Args.At, job.Args.Preview)
	// A failed output write must not retry already committed deletions solely for reporting.
	if outputErr := river.RecordOutput(ctx, result); outputErr != nil {
		log.Error().Err(outputErr).Msg("record retention sweep output")
	}

	return err
}
