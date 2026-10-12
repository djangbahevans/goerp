package jobqueue

import (
	"time"

	"github.com/riverqueue/river"
)

type RetentionSweepArgs struct {
	At      time.Time `json:"at"`
	Preview bool      `json:"preview,omitzero"`
}

func (RetentionSweepArgs) Kind() string { return "retention_sweep" }

func (RetentionSweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}
