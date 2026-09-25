package event

import (
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

// Envelope is the wire shape a handle_event invocation carries — every
// caller of ModuleInstance.InvokeHandleEvent (internal/engine/
// eventdelivery.SubscriberDeliveryWorker for async delivery,
// internal/engine/eventdelivery.SyncDispatcher for inline synchronous
// dispatch) marshals one of these instead of passing the bare event
// payload, so the module's own handle_event export has the event name it
// needs to route to the handler registered via engine.OnEvent.
type Envelope abiv1.EventEnvelope

// Marshal encodes e as the msgpack bytes InvokeHandleEvent's payload
// argument expects.
func (e Envelope) Marshal() ([]byte, error) {
	return msgpack.Marshal(e)
}
