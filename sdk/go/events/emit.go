package events

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/vmihailenco/msgpack/v5"
)

type eventEmitTxInput struct {
	TxID           string `msgpack:"tx_id"`
	Name           string `msgpack:"name"`
	Version        int    `msgpack:"version"`
	Payload        []byte `msgpack:"payload"`
	DelayMs        int    `msgpack:"delay_ms,omitempty"`
	IdempotencyKey string `msgpack:"idempotency_key,omitempty"`
	Sync           bool   `msgpack:"sync,omitempty"`
}

type eventEmitInput struct {
	Name           string `msgpack:"name"`
	Version        int    `msgpack:"version"`
	Payload        []byte `msgpack:"payload"`
	DelayMs        int    `msgpack:"delay_ms,omitempty"`
	IdempotencyKey string `msgpack:"idempotency_key,omitempty"`
	Sync           bool   `msgpack:"sync,omitempty"`
}

type eventEmitOutput struct {
	EventID string `msgpack:"event_id"`
}

type eventEmitTxOutput struct {
	EventID string `msgpack:"event_id"`
}

// EmitOption configures Emit/EmitTx — WithVersion, WithDelay, WithSync,
// WithIdempotencyKey.
type EmitOption func(*eventEmitInput)

// WithVersion sets the emitted event's schema version. Unset defaults to
// 0.
func WithVersion(v int) EmitOption {
	return func(in *eventEmitInput) { in.Version = v }
}

// WithDelay defers delivery by d.
func WithDelay(d time.Duration) EmitOption {
	return func(in *eventEmitInput) { in.DelayMs = int(d / time.Millisecond) }
}

// WithSync dispatches the event's synchronous (async:false) subscribers
// inline before Emit returns, surfacing any aggregated subscriber
// failure as the returned error. Emit only — combining it with EmitTx is
// rejected by host.event.emit_tx (event.sync_not_allowed).
func WithSync() EmitOption {
	return func(in *eventEmitInput) { in.Sync = true }
}

// WithIdempotencyKey supplies an explicit dedup key, taking precedence
// over any manifest-declared idempotency_key_field for this event
// (event-system.md §4).
func WithIdempotencyKey(key string) EmitOption {
	return func(in *eventEmitInput) { in.IdempotencyKey = key }
}

func applyOpts(in *eventEmitInput, opts []EmitOption) {
	for _, opt := range opts {
		opt(in)
	}
}

// Emit emits an event via host.event.emit, msgpack-encoding payload as
// the event's payload. Returns the emitted event's ID.
func Emit(name string, payload any, opts ...EmitOption) (string, error) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}

	in := eventEmitInput{Name: name, Payload: data}
	applyOpts(&in, opts)

	var out eventEmitOutput
	if err := hostcall.Do(hostEventEmit, in, &out); err != nil {
		return "", err
	}
	return out.EventID, nil
}

// EmitTx emits an event scoped to tx via host.event.emit_tx — visible to
// other work in the same transaction, delivered only once tx commits.
// WithSync is not supported here (see WithSync's own doc comment).
func EmitTx(tx *db.Tx, name string, payload any, opts ...EmitOption) (string, error) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}

	var in eventEmitInput
	applyOpts(&in, opts)

	out := eventEmitTxOutput{}
	txIn := eventEmitTxInput{
		TxID: tx.TxID(), Name: name, Version: in.Version, Payload: data,
		DelayMs: in.DelayMs, IdempotencyKey: in.IdempotencyKey, Sync: in.Sync,
	}
	if err := hostcall.Do(hostEventEmitTx, txIn, &out); err != nil {
		return "", err
	}
	return out.EventID, nil
}
