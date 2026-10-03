package engine

import (
	"fmt"

	"github.com/djangbahevans/goerp/sdk/go/jobs"
)

// UserID is empty for jobs without an initiating user. Host calls resolve its
// current permissions independently of the HTTP session that enqueued the job.
type JobContext struct {
	JobID       string
	JobType     string
	TenantID    string
	UserID      string
	TraceID     string
	Attempt     int
	MaxAttempts int
}

// jobHandler is an OnJob handler with its typed payload argument erased,
// so handlers of different payload types share one registry.
type jobHandler func(ctx *JobContext, payload []byte) error

var jobHandlers = map[string]jobHandler{}

// OnJob registers fn to run when a job of jobType (one of the module's
// manifest job_types[] names) arrives, called in init(). The job's
// msgpack payload is decoded into fn's T argument before fn runs; a
// payload that doesn't decode into T fails the job permanently, since
// retrying can't fix it. An empty payload leaves T at its zero value.
func OnJob[T any](jobType string, fn func(ctx *JobContext, payload T) error) {
	jobHandlers[jobType] = func(ctx *JobContext, payload []byte) error {
		var p T
		if len(payload) > 0 {
			if err := unmarshal(payload, &p); err != nil {
				return jobs.PermanentError(fmt.Errorf("decode %s payload: %w", jobType, err))
			}
		}
		return fn(ctx, p)
	}
}
