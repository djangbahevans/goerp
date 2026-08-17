package jobqueue

import (
	"context"

	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// ProbeArgs is a synthetic job type proving River's plumbing end-to-end —
// not a real business job type. Future job types should follow the same
// IdempotencyKey + `river:"unique"` pattern.
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
