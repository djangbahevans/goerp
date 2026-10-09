// Command connectorfixture exercises SMS and push provider handlers in WASM.
package main

import (
	"errors"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/djangbahevans/goerp/sdk/go/notify"
)

type observed struct {
	JobType string                 `msgpack:"job_type"`
	Attempt int                    `msgpack:"attempt"`
	SMS     notify.SMSSendPayload  `msgpack:"sms"`
	Push    notify.PushSendPayload `msgpack:"push"`
}

// parked keeps the observation out of reach of any started River client
// polling the shared default queue; the tests only read it.
var parked = jobs.WithDelay(time.Hour)

var observedJob = jobs.Define[observed]("connectorfixture_observed", jobs.Label("Fixture observation"))

func init() {
	engine.HandleProviderJob(notify.SMSSend, func(ctx *engine.JobContext, p notify.SMSSendPayload) error {
		if _, err := observedJob.Enqueue(observed{JobType: ctx.JobType, Attempt: ctx.Attempt, SMS: p}, parked); err != nil {
			return err
		}
		return outcome(p.Body)
	})
	engine.HandleProviderJob(notify.PushSend, func(ctx *engine.JobContext, p notify.PushSendPayload) error {
		if _, err := observedJob.Enqueue(observed{JobType: ctx.JobType, Attempt: ctx.Attempt, Push: p}, parked); err != nil {
			return err
		}
		if strings.Contains(p.Title, "correct") {
			if err := notify.UpdateDeliveryStatus(p.NotificationID, notify.ChannelPush, "tok-b", "failed", "unregistered token"); err != nil {
				return err
			}
			if err := notify.RemoveDeviceToken("tok-b"); err != nil {
				return err
			}
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
