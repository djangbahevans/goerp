package engine

import (
	"errors"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/events"
)

// wireEvent is the msgpack shape DispatchEvent decodes — mirrors
// internal/engine/event.Envelope byte-for-byte. Independently defined,
// not shared via import, since the engine and a compiled module are
// separate binaries; keep both in sync by hand if this shape ever
// changes.
type wireEvent struct {
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

// DispatchEvent is what a module's handle_event export calls
// (manifest-spec.md §26): decode the incoming wire envelope, look up the
// handler registered via OnEvent by event name, invoke it, and return
// the bare i32 status handle_event's ABI reserves (goerp#129,
// internal/engine/wasm.ModuleInstance.InvokeHandleEvent's own doc
// comment) — 0 success, 1 ordinary retryable failure, 2 permanent
// failure. events.RetryAfter's custom delay is not carried over this
// ABI (see events.RetryAfter's own doc comment): it degrades to status
// 1, using the subscription's own declared retry_policy backoff instead
// of the caller-specified duration.
func DispatchEvent(ptr, length uint32) uint32 {
	buf := ReadMem(ptr, length)

	var wire wireEvent
	if err := unmarshal(buf, &wire); err != nil {
		return 1
	}

	handler, ok := eventHandlers[wire.Name]
	if !ok {
		return 1
	}

	evt := events.NewEvent(wire.ID, wire.Name, wire.Version, wire.EmitterModule, wire.TenantID, wire.UserID, wire.TraceID, wire.EmittedAt, wire.Payload)

	err := handler(evt)
	if err == nil {
		return 0
	}
	if _, ok := errors.AsType[*events.PermanentDeliveryError](err); ok {
		return 2
	}
	return 1
}
