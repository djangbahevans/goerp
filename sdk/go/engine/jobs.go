package engine

import (
	"fmt"
	"slices"

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

// JobRegistration records one HandleJob registration for manifest generation.
type JobRegistration struct {
	Definition jobs.Definition
	// Handler is the SDK routing name written to the manifest's handler.
	Handler string
}

// jobHandler is a HandleJob handler with its typed payload argument erased,
// so handlers of different payload types share one registry.
type jobHandler func(ctx *JobContext, payload []byte) error

var (
	jobHandlers      = map[string]jobHandler{}
	jobRegistrations []JobRegistration
)

// HandleJob registers handler to run when a job of def's type arrives,
// called in init(). The job's msgpack payload is decoded into P before
// handler runs; a payload that doesn't decode into P fails the job
// permanently, since retrying can't fix it, and handler is not called. An
// empty payload leaves P at its zero value. It panics when handler is nil or
// def is the zero value or already has a handler, so a duplicate registration fails when the
// module loads, not when the job arrives.
func HandleJob[P any](def jobs.Def[P], handler func(ctx *JobContext, payload P) error) {
	name := def.Name()
	if name == "" {
		panic("engine.HandleJob: job definition is the zero value; build it with jobs.Define")
	}
	if handler == nil {
		panic(fmt.Sprintf("engine.HandleJob: job %q has a nil handler", name))
	}
	if _, dup := jobHandlers[name]; dup {
		panic(fmt.Sprintf("engine.HandleJob: job %q is already registered", name))
	}
	jobHandlers[name] = func(ctx *JobContext, payload []byte) error {
		var p P
		if len(payload) > 0 {
			if err := unmarshal(payload, &p); err != nil {
				return jobs.PermanentError(fmt.Errorf("decode %s payload: %w", name, err))
			}
		}
		return handler(ctx, p)
	}
	jobRegistrations = append(jobRegistrations, JobRegistration{Definition: def, Handler: handlerName(handler, "handle_job")})
}

// JobRegistrations returns every HandleJob registration in registration
// order.
func JobRegistrations() []JobRegistration {
	return slices.Clone(jobRegistrations)
}
