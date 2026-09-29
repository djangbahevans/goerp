// Command connectorfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/notify's provider delivery tests: an SMS and push
// connector that handles sms_send and push_send with engine.OnJob and the
// SDK's notify payload types, as a real provider connector does. Each
// handler reports the payload it decoded by enqueueing a
// connectorfixture_observed job, then succeeds, unless the rendered body
// says "fail" (a plain error) or "permanent" (jobs.PermanentError).
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o connectorfixture.wasm .
package main

import (
	"errors"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/djangbahevans/goerp/sdk/go/notify"
)

// observed is connectorfixture_observed's payload.
type observed struct {
	JobType string                 `msgpack:"job_type"`
	Attempt int                    `msgpack:"attempt"`
	SMS     notify.SMSSendPayload  `msgpack:"sms"`
	Push    notify.PushSendPayload `msgpack:"push"`
}

// parked keeps the observation out of reach of any started River client
// polling the shared default queue; the tests only read it.
var parked = jobs.WithDelay(time.Hour)

func init() {
	engine.OnJob(notify.JobTypeSMSSend, func(ctx *engine.JobContext, p notify.SMSSendPayload) error {
		if _, err := jobs.Enqueue("connectorfixture_observed", observed{JobType: ctx.JobType, Attempt: ctx.Attempt, SMS: p}, parked); err != nil {
			return err
		}
		return outcome(p.Body)
	})
	engine.OnJob(notify.JobTypePushSend, func(ctx *engine.JobContext, p notify.PushSendPayload) error {
		if _, err := jobs.Enqueue("connectorfixture_observed", observed{JobType: ctx.JobType, Attempt: ctx.Attempt, Push: p}, parked); err != nil {
			return err
		}
		return outcome(p.Title)
	})
}

func outcome(text string) error {
	switch {
	case strings.Contains(text, "permanent"):
		return jobs.PermanentError(errors.New("provider rejected the recipient"))
	case strings.Contains(text, "fail"):
		return errors.New("provider unavailable")
	}
	return nil
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
