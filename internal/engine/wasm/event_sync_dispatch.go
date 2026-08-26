package wasm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/event"
)

// SyncEventDispatcher resolves an async:false event subscriber's owning
// module by name and invokes its handle_event export synchronously,
// returning the reserved status code ModuleInstance.InvokeHandleEvent's
// doc comment defines (0/1/2). Implemented outside this package (by
// internal/engine/eventdelivery, goerp#129) and injected via
// Runtime.SetSyncEventDispatcher — the wasm package cannot resolve other
// modules by name itself, since internal/engine/registry (which can)
// imports internal/engine/module, which imports this package; importing
// registry back here would cycle. This is the same import-cycle shape
// internal/engine/jobdispatch and internal/engine/eventdelivery's own
// package doc comments already document for the analogous problem.
type SyncEventDispatcher interface {
	// ctx already carries the per-subscriber deadline dispatchSyncSubscribers
	// applies — DispatchSync does not need to apply its own.
	DispatchSync(ctx context.Context, moduleName, handlerName string, payload []byte) (status int32, err error)
}

// subscriberOutcomeError is one async:false subscriber's contribution to
// dispatchSyncSubscribers' aggregated failure — either the plain error
// InvokeHandleEvent/DispatchSync returned, or (when the subscriber's own
// timeout elapsed) a subscriber_timeout error, per event-system.md §8
// "Fan-out and timeout".
type subscriberOutcomeError struct {
	ModuleName  string
	HandlerName string
	Err         error
}

func (e *subscriberOutcomeError) Error() string {
	return fmt.Sprintf("%s.%s: %v", e.ModuleName, e.HandlerName, e.Err)
}

func (e *subscriberOutcomeError) Unwrap() error { return e.Err }

// dispatchSyncSubscribers runs every async:false subscriber of eventName
// sequentially, in registration order (event-system.md §8: "they run
// sequentially, in registration order... not in parallel"), each under
// its own timeout. A subscriber's failure or timeout does not stop the
// remaining ones from running — every outcome is aggregated into one
// combined error via errors.Join, returned only if at least one
// subscriber failed.
func dispatchSyncSubscribers(ctx context.Context, dispatcher SyncEventDispatcher, reg *event.EventRegistry, eventName string, payload []byte, timeout time.Duration) error {
	var failures []error
	total := 0

	for _, sub := range reg.Subscribers(eventName) {
		if sub.Async {
			continue
		}
		total++

		subCtx, cancel := context.WithTimeout(ctx, timeout)
		status, err := dispatcher.DispatchSync(subCtx, sub.ModuleName, sub.HandlerName, payload)
		timedOut := subCtx.Err() == context.DeadlineExceeded
		cancel()

		switch {
		case timedOut:
			failures = append(failures, &subscriberOutcomeError{
				ModuleName: sub.ModuleName, HandlerName: sub.HandlerName,
				Err: fmt.Errorf("subscriber_timeout: exceeded %s", timeout),
			})
		case err != nil:
			failures = append(failures, &subscriberOutcomeError{ModuleName: sub.ModuleName, HandlerName: sub.HandlerName, Err: err})
		case status == 2: // permanent — see ModuleInstance.InvokeHandleEvent's doc comment
			failures = append(failures, &subscriberOutcomeError{
				ModuleName: sub.ModuleName, HandlerName: sub.HandlerName,
				Err: errors.New("handler returned a permanent failure"),
			})
		case status != 0:
			failures = append(failures, &subscriberOutcomeError{
				ModuleName: sub.ModuleName, HandlerName: sub.HandlerName,
				Err: fmt.Errorf("handler returned status %d", status),
			})
		}
	}

	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d synchronous subscribers failed: %w", len(failures), total, errors.Join(failures...))
}
