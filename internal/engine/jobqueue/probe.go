package jobqueue

import (
	"context"

	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// ProbeArgs exercises queue plumbing with an idempotency key marked for River uniqueness.
type ProbeArgs struct {
	IdempotencyKey string `json:"idempotency_key" river:"unique"`
	Message        string `json:"message"`
}

func (ProbeArgs) Kind() string { return "probe" }

func (ProbeArgs) InsertOpts() river.InsertOpts {
	return UniqueByIdempotencyKey()
}

type ProbeWorker struct {
	river.WorkerDefaults[ProbeArgs]
}

func (w *ProbeWorker) Work(ctx context.Context, job *river.Job[ProbeArgs]) error {
	log.Info().Str("message", job.Args.Message).Msg("probe job executed")
	return nil
}
