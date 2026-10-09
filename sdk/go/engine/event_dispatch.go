package engine

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/events"
)

// DispatchEvent is what a module's handle_event export calls: decode the
// incoming envelope, invoke the subscription registered via Subscribe or SubscribeTx for
// its (event name, version), and return a status: 0 success, 1 retryable
// failure, 2 permanent failure. An events.RetryAfter delay is not carried
// through; it is retried with the subscription's declared retry_policy
// backoff.
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
