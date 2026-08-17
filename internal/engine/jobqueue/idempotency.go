package jobqueue

import "github.com/riverqueue/river"

// UniqueByIdempotencyKey returns InsertOpts making a job a no-op while an
// identical idempotency key is already pending. The args struct's key
// field must be tagged `river:"unique"` (see ProbeArgs.IdempotencyKey).
func UniqueByIdempotencyKey() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
}
