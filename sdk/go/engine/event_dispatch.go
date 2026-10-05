package engine

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/events"
)

// DispatchEvent is what a module's handle_event export calls
// (manifest-spec.md §26): decode the incoming wire envelope, look up the
// subscription registered via Subscribe by (event name, version), invoke it, and return
// the bare i32 status handle_event's ABI reserves (goerp#129,
// internal/engine/wasm.ModuleInstance.InvokeHandleEvent's own doc
// comment) — 0 success, 1 ordinary retryable failure, 2 permanent
// failure. events.RetryAfter's custom delay is not carried over this
// ABI (see events.RetryAfter's own doc comment): it degrades to status
// 1, using the subscription's own declared retry_policy backoff instead
// of the caller-specified duration.
func DispatchEvent(ptr, length uint32) uint32 {
	buf := ReadMem(ptr, length)

	var wire abi.EventEnvelope
	if err := unmarshal(buf, &wire); err != nil {
		return 1
	}

	sub, ok := subscriptions[subscriptionKey{wire.Name, wire.Version}]
	if !ok {
		return 1
	}

	err := sub.invoke(wire)
	if err == nil {
		return 0
	}
	if _, ok := errors.AsType[*events.PermanentDeliveryError](err); ok {
		return 2
	}
	return 1
}
