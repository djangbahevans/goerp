package jobqueue

import (
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// UniqueByIdempotencyKey returns InsertOpts making a job a no-op while an
// identical idempotency key is already pending. The args struct's key
// field must be tagged `river:"unique"` (see ProbeArgs.IdempotencyKey).
func UniqueByIdempotencyKey() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// UniqueAcrossAllJobStates is every rivertype.JobState a UniqueOpts.ByState
// list needs in order to dedupe against a job's entire lifetime, not just
// its "active" states. River's own default ByState only counts
// Available/Pending/Running/Scheduled/Retryable — sufficient for
// preventing a duplicate insert while the original job is still in
// flight, but not for the idempotency guarantee an event/subscriber
// delivery job needs: a retry arriving after the original job already
// reached a terminal state (Completed, Discarded) must still see it and
// dedupe against it, or the retry inserts a second, duplicate job.
// Available/Pending/Running/Scheduled are required by river v0.43.0's own
// UniqueOpts.validate() — omitting any of them is a hard insert-time
// error, not just a missed dedup case.
var UniqueAcrossAllJobStates = []rivertype.JobState{
	rivertype.JobStateAvailable, rivertype.JobStatePending,
	rivertype.JobStateRunning, rivertype.JobStateScheduled,
	rivertype.JobStateRetryable, rivertype.JobStateCompleted,
	rivertype.JobStateDiscarded,
}
