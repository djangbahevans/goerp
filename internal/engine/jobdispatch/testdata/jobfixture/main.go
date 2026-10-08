// Command jobfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/jobdispatch's ordinary-job tests (goerp#1302). It
// declares its handlers with the SDK's actual engine.HandleJob and dispatches
// through engine.DispatchJob. Handlers report what they saw by enqueueing
// a jobfixture_observed job through a jobs.Def, so a test reads the
// result back from the job queue without any other host capability.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o jobfixture.wasm .
package main

import (
	"errors"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
)

// workPayload is jobfixture_start's and jobfixture_work's payload. Mode
// selects jobfixture_work's return value: "" succeeds, "fail" returns a
// plain error and "permanent" returns jobs.PermanentError.
type workPayload struct {
	Note string `msgpack:"note"`
	Mode string `msgpack:"mode"`
}

// observed is jobfixture_observed's payload: the JobContext and decoded
// payload jobfixture_work ran with.
type observed struct {
	JobID       string `msgpack:"job_id"`
	JobType     string `msgpack:"job_type"`
	TenantID    string `msgpack:"tenant_id"`
	TraceID     string `msgpack:"trace_id"`
	Attempt     int    `msgpack:"attempt"`
	MaxAttempts int    `msgpack:"max_attempts"`
	Note        string `msgpack:"note"`
}

// parked schedules every job this fixture enqueues far enough ahead that
// no started River client polling the shared default queue (another
// package's test engine) claims it; the tests work these rows directly.
var parked = jobs.WithDelay(time.Hour)

var (
	startJob    = jobs.Define[workPayload]("jobfixture_start", jobs.Label("Fixture start"))
	workJob     = jobs.Define[workPayload]("jobfixture_work", jobs.Label("Fixture work"))
	hangJob     = jobs.Define[workPayload]("jobfixture_hang", jobs.Label("Fixture hang"))
	observedJob = jobs.Define[observed]("jobfixture_observed", jobs.Label("Fixture observation"))
)

// Cron jobs: jobfixture_cron succeeds and reports the CronContext it ran
// with, the _fail and _permanent variants return a plain and a permanent
// error, and jobfixture_cron_unregistered is declared but has no handler.
var (
	cronJob          = jobs.DefineCron("jobfixture_cron", jobs.Label("Fixture cron"), jobs.Schedule("* * * * *"))
	cronFailJob      = jobs.DefineCron("jobfixture_cron_fail", jobs.Label("Fixture failing cron"), jobs.Schedule("* * * * *"))
	cronPermanentJob = jobs.DefineCron("jobfixture_cron_permanent", jobs.Label("Fixture permanent cron"), jobs.Schedule("* * * * *"))
	cronHangJob      = jobs.DefineCron("jobfixture_cron_hang", jobs.Label("Fixture hanging cron"), jobs.Schedule("* * * * *"))
	_                = jobs.DefineCron("jobfixture_cron_unregistered", jobs.Label("Fixture unregistered cron"), jobs.Schedule("* * * * *"))
)

// hang never returns, so only the engine's timeout can end the call.
func hang() {
	for {
	}
}

func init() {
	engine.HandleCron(cronJob, func(ctx *jobs.CronContext) error {
		_, err := observedJob.Enqueue(observed{JobType: cronJob.Name(), TenantID: ctx.TenantID, TraceID: ctx.TraceID}, parked)
		return err
	})
	engine.HandleCron(cronHangJob, func(*jobs.CronContext) error {
		hang()
		return nil
	})
	engine.HandleJob(hangJob, func(*engine.JobContext, workPayload) error {
		hang()
		return nil
	})
	engine.HandleCron(cronFailJob, func(*jobs.CronContext) error {
		return errors.New("intentional cron failure for testing")
	})
	engine.HandleCron(cronPermanentJob, func(*jobs.CronContext) error {
		return jobs.PermanentError(errors.New("intentional permanent cron failure for testing"))
	})

	engine.HandleJob(startJob, func(_ *engine.JobContext, p workPayload) error {
		_, err := workJob.Enqueue(p, jobs.WithMaxAttempts(4), parked)
		return err
	})
	engine.HandleJob(workJob, func(ctx *engine.JobContext, p workPayload) error {
		if _, err := observedJob.Enqueue(observed{
			JobID: ctx.JobID, JobType: ctx.JobType, TenantID: ctx.TenantID, TraceID: ctx.TraceID,
			Attempt: ctx.Attempt, MaxAttempts: ctx.MaxAttempts, Note: p.Note,
		}, parked); err != nil {
			return err
		}
		switch p.Mode {
		case "fail":
			return errors.New("intentional failure for testing")
		case "permanent":
			return jobs.PermanentError(errors.New("intentional permanent failure for testing"))
		}
		return nil
	})
}

//go:wasmexport handle_job
func handleJob(ptr, length uint32) uint32 {
	return engine.DispatchJob(ptr, length)
}

//go:wasmexport handle_cron
func handleCron(ptr, length uint32) uint32 {
	return engine.DispatchCron(ptr, length)
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
