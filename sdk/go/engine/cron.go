package engine

import (
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
)

var (
	cronHandlers      = map[string]func(*jobs.CronContext) error{}
	cronRegistrations []jobs.CronDef
)

// HandleCron registers fn to run when def's schedule fires, called in
// init(). It panics when fn is nil or def already has a handler, so a
// duplicate registration fails when the module loads, not when the schedule
// fires.
func HandleCron(def jobs.CronDef, fn func(*jobs.CronContext) error) {
	name := def.Name()
	if fn == nil {
		panic(fmt.Sprintf("engine.HandleCron: cron %q has a nil handler", name))
	}
	if _, dup := cronHandlers[name]; dup {
		panic(fmt.Sprintf("engine.HandleCron: cron %q is already registered", name))
	}
	cronHandlers[name] = fn
	cronRegistrations = append(cronRegistrations, def)
}

// CronRegistrations returns the cron definitions registered with
// HandleCron, in registration order, for manifest generation.
func CronRegistrations() []jobs.CronDef {
	return append([]jobs.CronDef(nil), cronRegistrations...)
}

// DispatchCron is what a module's handle_cron export calls. The engine
// sends the JobEnvelope of the cron job it enqueued, whose JobType is the
// cron name. It returns handle_job's status codes: 0 success, 1 retryable
// failure, 2 permanent failure.
func DispatchCron(ptr, length uint32) uint32 {
	var env abi.JobEnvelope
	if err := unmarshal(ReadMem(ptr, length), &env); err != nil {
		return 1
	}

	handler, ok := cronHandlers[env.JobType]
	if !ok {
		return 1
	}
	return jobStatus(handler(&jobs.CronContext{TenantID: env.TenantID, TraceID: env.TraceID}))
}
