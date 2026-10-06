package engine

import (
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/declare"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	jobdef "github.com/djangbahevans/goerp/sdk/go/jobs/def"
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

// jobHandler is a HandleJob handler with its typed payload argument erased,
// so handlers of different payload types share one registry.
type jobHandler func(ctx *JobContext, payload []byte) error

var jobHandlers = map[string]jobHandler{}

// HandleJob registers handler to run when a job of def's type arrives,
// called in init(). The job's msgpack payload is decoded into P before
// handler runs; a payload that doesn't decode into P fails the job
// permanently, since retrying can't fix it, and handler is not called. An
// empty payload leaves P at its zero value. It panics when handler is nil or
// def is the zero value or already has a handler, so a duplicate
// registration fails when the module loads, not when the job arrives.
func HandleJob[P any](def jobs.Def[P], handler func(ctx *JobContext, payload P) error) {
	registerJobHandler("HandleJob", def.Name(), handler)
	declare.Add(jobdef.KindJobHandler, jobdef.HandlerDeclaration{Name: def.Name(), Handler: routingName(handler, def.Name())})
}

// HandleProviderJob registers a connector's handler for the platform-owned
// provider-category job def, called in init(), with HandleJob's decoding and
// registration rules. The same handler serves queued and synchronously
// dispatched deliveries. The registration generates no job_types entry,
// since the platform, not the module, owns the job type.
func HandleProviderJob[P, R any](def jobs.ProviderDef[P, R], handler func(ctx *JobContext, payload P) error) {
	registerJobHandler("HandleProviderJob", def.Name(), handler)
}

// registerJobHandler is the registry HandleJob and HandleProviderJob share,
// so one job type name has one handler across both. fn names the public
// caller in panic messages.
func registerJobHandler[P any](fn, name string, handler func(*JobContext, P) error) {
	if name == "" {
		panic(fmt.Sprintf("engine.%s: job definition is the zero value; build it with jobs.Define or jobs.DefineProvider", fn))
	}
	if handler == nil {
		panic(fmt.Sprintf("engine.%s: job %q has a nil handler", fn, name))
	}
	if _, dup := jobHandlers[name]; dup {
		panic(fmt.Sprintf("engine.%s: job %q is already registered", fn, name))
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
}

// routingName is the handler's function name for the manifest. An anonymous
// function's generated name shifts with its siblings, so the definition's
// name stands in.
func routingName(handler any, definition string) string {
	name := handlerName(handler, definition)
	if strings.Contains(name, ".func") || strings.HasSuffix(name, "-fm") {
		return definition
	}
	return name
}
