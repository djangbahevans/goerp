package event

import (
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

// Envelope is the wire shape a handle_event invocation carries — every
// caller of ModuleInstance.InvokeHandleEvent (internal/engine/
// eventdelivery.SubscriberDeliveryWorker for async delivery,
// internal/engine/eventdelivery.SyncDispatcher for inline synchronous
// dispatch) marshals one of these instead of passing the bare event
// payload, so the module's own handle_event export — a single, fixed
// export regardless of how many events the module subscribes to — has
// enough information (chiefly Name) to route to the handler registered
// via the module's own engine.OnEvent(name, ...) call. sdk/go/events'
// DispatchEvent decodes an identical wire shape (msgpack tags must match
// byte-for-byte) — the two are independently defined, not shared via
// import, since the engine and a compiled module are separate binaries;
// keep both in sync by hand if this shape ever changes.
type Envelope struct {
	ID            string    `msgpack:"id"`
	Name          string    `msgpack:"name"`
	Version       int       `msgpack:"version"`
	EmitterModule string    `msgpack:"emitter_module"`
	TenantID      string    `msgpack:"tenant_id"`
	UserID        string    `msgpack:"user_id,omitempty"`
	TraceID       string    `msgpack:"trace_id,omitempty"`
	EmittedAt     time.Time `msgpack:"emitted_at"`
	Payload       []byte    `msgpack:"payload"`
}

// Marshal encodes e as the msgpack bytes InvokeHandleEvent's payload
// argument expects.
func (e Envelope) Marshal() ([]byte, error) {
	return msgpack.Marshal(e)
}
