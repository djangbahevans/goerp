package eventdelivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/riverqueue/river"
)

const (
	statusRetryable = 1
	statusPermanent = 2
)

type SubscriberDeliveryWorker struct {
	river.WorkerDefaults[jobqueue.SubscriberDeliveryArgs]
	ModuleRegistry *registry.ModuleRegistry
	Invoker        *HandlerInvoker
}

func (w *SubscriberDeliveryWorker) Work(ctx context.Context, job *river.Job[jobqueue.SubscriberDeliveryArgs]) error {
	args := job.Args

	snap := w.ModuleRegistry.Snapshot()
	if snap == nil {
		return fmt.Errorf("module registry has no snapshot yet")
	}

	if !isLiveAsyncSubscriber(snap.EventRegistry().Subscribers(args.EventName), args.ModuleName, args.HandlerName) {
		return fmt.Errorf("subscription %s.%s for event %q is no longer a registered async subscriber", args.ModuleName, args.HandlerName, args.EventName)
	}

	mod, ok := snap.Modules()[args.ModuleName]
	if !ok || mod.Status != module.StatusReady {
		return fmt.Errorf("module %q is not ready", args.ModuleName)
	}

	if mod.Pool == nil {
		return fmt.Errorf("module %q has no WASM instance pool (wasm: false)", args.ModuleName)
	}

	status, err := w.Invoker.invoke(ctx, snap, mod, event.Envelope{
		ID: args.EventID, Name: args.EventName, Version: args.EventVersion,
		EmitterModule: args.EmitterModule, TenantID: args.TenantID, UserID: args.UserID,
		TraceID: args.TraceID, EmittedAt: args.EmittedAt, Payload: args.Payload,
	})
	if err != nil {
		if errors.Is(err, role.ErrNotMember) {
			return river.JobCancel(err)
		}
		return err
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

// Retry policy is resolved from the live subscription so reloads apply to retries.
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
