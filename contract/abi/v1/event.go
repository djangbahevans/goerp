package abi

import "time"

// EventEnvelope is the wire shape a handle_event invocation carries. The
// engine wraps every delivery in one, so the module's single handle_event
// export has the event name it needs to route to the handler registered for
// that name.
type EventEnvelope struct {
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
