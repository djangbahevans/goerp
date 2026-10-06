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
	observedJob = jobs.Define[observed]("jobfixture_observed", jobs.Label("Fixture observation"))
)

func init() {
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

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
