package eventdelivery

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/riverqueue/river"
)

// statusPermanent/statusRetryable mirror the reserved handle_event status
// codes ModuleInstance.InvokeHandleEvent's doc comment defines (goerp#129)
// — 0 is success and handled inline below, so only the two failure codes
// need names here.
const (
	statusRetryable = 1
	statusPermanent = 2
)

// SubscriberDeliveryWorker processes jobqueue.SubscriberDeliveryArgs jobs —
// the piece that actually invokes an async subscriber's handler, closing
// the gap Worker's fan-out (worker.go) leaves: nothing else in this
// codebase resolves the target module and calls its handle_event export
// for these jobs. Shares jobdispatch.Worker's exact shape (nil-Snapshot
// guard, StatusReady/nil-Pool checks, Pool.Borrow/Return) but resolves
// against the live EventRegistry instead of the JobRegistry, since a
// subscriber delivery is scoped to (EventName, ModuleName, HandlerName),
// not a job type.
type SubscriberDeliveryWorker struct {
	river.WorkerDefaults[jobqueue.SubscriberDeliveryArgs]
	ModuleRegistry *registry.ModuleRegistry
}

func (w *SubscriberDeliveryWorker) Work(ctx context.Context, job *river.Job[jobqueue.SubscriberDeliveryArgs]) error {
	args := job.Args

	snap := w.ModuleRegistry.Snapshot()
	if snap == nil {
		return fmt.Errorf("module registry has no snapshot yet")
	}

	if !isLiveAsyncSubscriber(snap.EventRegistry().Subscribers(args.EventName), args.ModuleName, args.HandlerName) {
		// The subscription named at insert time is no longer declared (a
		// manifest change removed or renamed it, or async flipped to
		// false) — not retryable, the mismatch won't resolve itself on a
		// later attempt.
		return fmt.Errorf("subscription %s.%s for event %q is no longer a registered async subscriber", args.ModuleName, args.HandlerName, args.EventName)
	}

	mod, ok := snap.Modules()[args.ModuleName]
	if !ok || mod.Status != module.StatusReady {
		return fmt.Errorf("module %q is not ready", args.ModuleName)
	}
	if mod.Pool == nil {
		// A module manifest can legitimately declare wasm: false (e.g. the
		// "theme" type requires it) and still reach StatusReady with no
		// compiled WASM — an async subscription on such a module is a
		// manifest inconsistency nothing today rejects at load time; this
		// must not panic on mod.Pool.Borrow if it ever happens.
		return fmt.Errorf("module %q has no WASM instance pool (wasm: false)", args.ModuleName)
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return fmt.Errorf("borrow instance for %s: %w", args.ModuleName, err)
	}
	defer mod.Pool.Return(inst)

	envelope, err := event.Envelope{
		ID: args.EventID, Name: args.EventName, Version: args.EventVersion,
		EmitterModule: args.EmitterModule, TenantID: args.TenantID, UserID: args.UserID,
		TraceID: args.TraceID, EmittedAt: args.EmittedAt, Payload: args.Payload,
	}.Marshal()
	if err != nil {
		return fmt.Errorf("marshal event envelope: %w", err)
	}

	status, err := inst.InvokeHandleEvent(ctx, envelope)
	if err != nil {
		return fmt.Errorf("invoke handle_event for %s/%s: %w", args.ModuleName, args.HandlerName, err)
	}
	switch status {
	case 0:
		return nil
	case statusPermanent:
		return river.JobCancel(fmt.Errorf("handle_event for %s/%s returned a permanent failure", args.ModuleName, args.HandlerName))
	default:
		return fmt.Errorf("handle_event for %s/%s returned status %d", args.ModuleName, args.HandlerName, status)
	}
}

// NextRetry overrides River's client-level retry policy with the
// subscriber's own declared retry_policy (manifest-spec.md's RetryPolicy
// object) — looked up by (EventName, ModuleName, HandlerName) against the
// *current* snapshot, not a value captured at insert time, so a manifest
// change retroactively affects an in-flight retry the same way Work's own
// ownership check re-validates against the current snapshot on every
// attempt. Returns the zero time.Time (deferring to the client-level
// policy) when the subscription can no longer be found — Work will fail
// that same attempt with a non-retryable-in-spirit error anyway.
func (w *SubscriberDeliveryWorker) NextRetry(job *river.Job[jobqueue.SubscriberDeliveryArgs]) time.Time {
	snap := w.ModuleRegistry.Snapshot()
	if snap == nil {
		return time.Time{}
	}
	for _, sub := range snap.EventRegistry().Subscribers(job.Args.EventName) {
		if sub.ModuleName == job.Args.ModuleName && sub.HandlerName == job.Args.HandlerName && sub.Async {
			return computeBackoff(sub.RetryPolicy, job.Attempt)
		}
	}
	return time.Time{}
}

func isLiveAsyncSubscriber(subs []event.EventSubscription, moduleName, handlerName string) bool {
	for _, sub := range subs {
		if sub.ModuleName == moduleName && sub.HandlerName == handlerName && sub.Async {
			return true
		}
	}
	return false
}
