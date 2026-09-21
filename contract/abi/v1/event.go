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

// EventEmitTxInput is the request of host.event.emit_tx. Sync exists so the
// engine can reject it: emit_tx never honors it, and only host.event.emit
// can.
type EventEmitTxInput struct {
	TxID           string `msgpack:"tx_id"`
	Name           string `msgpack:"name"`
	Version        int    `msgpack:"version"`
	Payload        []byte `msgpack:"payload"`
	DelayMs        int    `msgpack:"delay_ms,omitempty"`
	IdempotencyKey string `msgpack:"idempotency_key,omitempty"`
	Sync           bool   `msgpack:"sync,omitempty"`
}

// EventEmitTxOutput is the response of host.event.emit_tx.
type EventEmitTxOutput struct {
	EventID string `msgpack:"event_id"`
}

// EventEmitInput is the request of host.event.emit: the shape of
// EventEmitTxInput without a transaction to scope the insert to.
type EventEmitInput struct {
	Name           string `msgpack:"name"`
	Version        int    `msgpack:"version"`
	Payload        []byte `msgpack:"payload"`
	DelayMs        int    `msgpack:"delay_ms,omitempty"`
	IdempotencyKey string `msgpack:"idempotency_key,omitempty"`
	Sync           bool   `msgpack:"sync,omitempty"`
}

// EventEmitOutput is the response of host.event.emit.
type EventEmitOutput struct {
	EventID string `msgpack:"event_id"`
}
