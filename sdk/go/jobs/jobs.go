// Package jobs is the module-side caller for the host.jobs namespace
// (host-abi-reference.md §10): Enqueue and EnqueueTx queue a background
// job of one of the module's own declared job_types.
package jobs

import (
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/vmihailenco/msgpack/v5"
)

// Queue names a job may be enqueued onto.
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueBulk     = "bulk"
	QueueEmail    = "email"
	QueueSearch   = "search"
)

// JobOption configures Enqueue/EnqueueTx. An unset option falls back to
// the job type's manifest declaration, then to the engine default.
type JobOption func(*abi.JobEnqueueOptions)

// OnQueue runs the job on queue instead of its manifest-declared queue.
func OnQueue(queue string) JobOption {
	return func(o *abi.JobEnqueueOptions) { o.Queue = queue }
}

// WithPriority sets the job's priority within its queue, 1-100, higher
// runs sooner.
func WithPriority(p int) JobOption {
	return func(o *abi.JobEnqueueOptions) { o.Priority = p }
}

// WithDelay runs the job no sooner than d from now. Cannot be combined
// with ScheduleAt.
func WithDelay(d time.Duration) JobOption {
	return func(o *abi.JobEnqueueOptions) { o.DelayMs = d.Milliseconds() }
}

// ScheduleAt runs the job no sooner than t, at one-second precision.
// Cannot be combined with WithDelay.
func ScheduleAt(t time.Time) JobOption {
	return func(o *abi.JobEnqueueOptions) { o.ScheduledAt = t.Unix() }
}

// WithMaxAttempts overrides the job type's manifest-declared max_attempts.
func WithMaxAttempts(n int) JobOption {
	return func(o *abi.JobEnqueueOptions) { o.MaxAttempts = n }
}

// WithIdempotencyKey deduplicates the job: enqueueing again with the same
// key while a job with that key still exists returns the existing job's
// ID instead of inserting a second one.
func WithIdempotencyKey(key string) JobOption {
	return func(o *abi.JobEnqueueOptions) { o.IdempotencyKey = key }
}

func buildOptions(opts []JobOption) abi.JobEnqueueOptions {
	var o abi.JobEnqueueOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Enqueue queues a jobType job via host.jobs.enqueue, msgpack-encoding
// payload as the job's payload. Returns the job's ID.
func Enqueue(jobType string, payload any, opts ...JobOption) (string, error) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}

	var out abi.JobsEnqueueOutput
	in := abi.JobsEnqueueInput{Type: jobType, Payload: data, Opts: buildOptions(opts)}
	if err := hostcall.Do(hostJobsEnqueue, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

// EnqueueTx queues a jobType job inside tx via host.jobs.enqueue_tx. The
// job becomes visible to workers only if tx commits.
func EnqueueTx(tx *db.Tx, jobType string, payload any, opts ...JobOption) (string, error) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}

	var out abi.JobsEnqueueOutput
	in := abi.JobsEnqueueTxInput{TxID: tx.TxID(), Type: jobType, Payload: data, Opts: buildOptions(opts)}
	if err := hostcall.Do(hostJobsEnqueueTx, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}
