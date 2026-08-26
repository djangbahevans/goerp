package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// idempotencyKeyNamespace is a fixed RFC 4122 namespace UUID for deriving
// deterministic event IDs from a caller-supplied idempotency key
// (engine-internals.md §9) — any fixed value works, it only needs to
// never change once chosen, since changing it would silently break dedup
// for any idempotency key already in flight across the 24h ByPeriod
// window.
var idempotencyKeyNamespace = uuid.MustParse("6f8c2b1e-6b8b-4f0a-9b1a-9c9e6a2d3b7c")

// registerHostEvent attaches host.event.emit_tx and host.event.emit to
// the runtime. Lives in the wasm package for the same import-cycle
// reason registerHostDB does — its closures need direct access to the
// Runtime's instance registry and an insert-capable River client.
//
// insertClient is a separate river.Client[*sql.Tx] (backed by
// riverdriver/riverdatabasesql, not the engine's own pgx-based
// jobqueue.Client) purely so InsertTx can accept the stdlib *sql.Tx
// modCtx.Transaction returns — the same transaction host.db.begin opened.
// A job inserted through this client is fully visible to and worked by
// the engine's existing pgx-based client once a real EventDeliveryWorker
// is registered (River's job table is driver-agnostic); this client is
// never Start()'d and never works jobs itself.
func registerHostEvent(ctx context.Context, rt wazero.Runtime, r *Runtime, insertClient *river.Client[*sql.Tx]) error {
	_, err := rt.NewHostModuleBuilder("host.event").
		NewFunctionBuilder().WithFunc(makeEventEmitTx(r, insertClient)).Export("emit_tx").
		NewFunctionBuilder().WithFunc(makeEventEmit(r, insertClient)).Export("emit").
		Instantiate(ctx)
	return err
}

type eventEmitTxInput struct {
	TxID           string `msgpack:"tx_id"`
	Name           string `msgpack:"name"`
	Version        int    `msgpack:"version"`
	Payload        []byte `msgpack:"payload"`
	DelayMs        int    `msgpack:"delay_ms,omitempty"`
	IdempotencyKey string `msgpack:"idempotency_key,omitempty"`
	// Sync exists on this input purely so emit_tx can reject it outright
	// (event-system.md §8: "EmitTx rejects WithSync() at call time... an
	// error returned before anything is inserted") — emit_tx never honors
	// it. host.event.emit is the only host function that can.
	Sync bool `msgpack:"sync,omitempty"`
}

// eventEmitInput is host.event.emit's (non-transactional) wire input —
// the same shape as eventEmitTxInput minus TxID, since there is no
// transaction to scope the insert to.
type eventEmitInput struct {
	Name           string `msgpack:"name"`
	Version        int    `msgpack:"version"`
	Payload        []byte `msgpack:"payload"`
	DelayMs        int    `msgpack:"delay_ms,omitempty"`
	IdempotencyKey string `msgpack:"idempotency_key,omitempty"`
	Sync           bool   `msgpack:"sync,omitempty"`
}

type eventEmitTxOutput struct {
	EventID string `msgpack:"event_id"`
}

func makeEventEmitTx(r *Runtime, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapEventEmit) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("event.emit"))
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input eventEmitTxInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		if input.Sync {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    abi.ErrCodeSyncNotAllowed,
				Message: "events.WithSync() is not permitted with EmitTx — use non-transactional Emit instead",
			})
		}

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeNoTransaction, Message: "tx_id does not exist or has expired"})
		}

		reg := modCtx.EventRegistry()
		if reg == nil || !reg.ModuleEmits(modCtx.ModuleName, input.Name) {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeUndeclared, Message: "event " + input.Name + " is not in this module's declared emits list"})
		}

		eventID := deriveEventID(reg, modCtx.ModuleName, modCtx.TenantID, input.Name, input.Payload, input.IdempotencyKey)

		err = insertEventDeliveryTx(ctx, insertClient, tx, eventID, input.Name, input.Version,
			modCtx.ModuleName, modCtx.TenantID, modCtx.UserID, modCtx.TraceID, input.Payload,
			time.Duration(input.DelayMs)*time.Millisecond,
			&river.UniqueOpts{
				// Bounded to 24h, not unbounded, so a legitimately new event
				// that happens to reuse an idempotency key after a long gap
				// isn't silently dropped forever. ByState uses
				// jobqueue.UniqueAcrossAllJobStates (not River's own
				// "active"-only default) so a retry arriving after the
				// original event already finished dispatching still sees
				// the prior job and dedupes against it, rather than
				// inserting a second, duplicate one.
				ByArgs:   true,
				ByPeriod: 24 * time.Hour,
				ByState:  jobqueue.UniqueAcrossAllJobStates,
			})
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true})
		}

		return abi.WriteToModule(ctx, m, allocate, eventEmitTxOutput{EventID: eventID.String()})
	}
}

// extractPayloadField best-effort-decodes payload (msgpack-encoded) and
// stringifies the named top-level field. A missing field, or a payload
// that isn't a map, just returns ok=false — the caller falls through to
// no dedup rather than treating this as an error.
func extractPayloadField(payload []byte, field string) (string, bool) {
	var decoded map[string]any
	if err := msgpack.Unmarshal(payload, &decoded); err != nil {
		return "", false
	}
	v, ok := decoded[field]
	if !ok || v == nil {
		return "", false
	}
	return fmt.Sprintf("%v", v), true
}

// deriveEventID resolves the eventID both emit host functions insert
// EventDeliveryArgs under: explicitKey wins if the caller supplied one
// (events.WithIdempotencyKey — event-system.md §4 "If both are present on
// the same emission, the emit-time WithIdempotencyKey value wins"),
// otherwise the manifest-declared emits[].idempotency_key_field is looked
// up and extracted from payload. A fresh random UUIDv7 is used when
// neither yields a key — the common case of an event with no natural
// idempotency key at all.
func deriveEventID(reg *event.EventRegistry, moduleName, tenantID, name string, payload []byte, explicitKey string) uuid.UUID {
	idempotencyKey := explicitKey
	if idempotencyKey == "" {
		if field, ok := reg.IdempotencyKeyField(moduleName, name); ok {
			idempotencyKey, _ = extractPayloadField(payload, field)
		}
	}
	if idempotencyKey == "" {
		return uuid.Must(uuid.NewV7())
	}
	return uuid.NewSHA1(idempotencyKeyNamespace, []byte(tenantID+"|"+name+"|"+idempotencyKey))
}

// eventEmitOutput is host.event.emit's wire output — the same shape as
// eventEmitTxOutput.
type eventEmitOutput struct {
	EventID string `msgpack:"event_id"`
}

// makeEventEmit builds host.event.emit, the non-transactional emit host
// function — the only one that can honor events.WithSync(): dispatching
// every async:false subscriber inline before the insert, sequentially,
// aggregating failures (event-system.md §8 "Fan-out and timeout"). An
// event_delivery job is always inserted afterward regardless of Sync,
// carrying SyncDispatched so eventdelivery.Worker's own fan-out knows
// whether the async:false subscribers it sees were already handled
// inline or still need an async fallback delivery.
func makeEventEmit(r *Runtime, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapEventEmit) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("event.emit"))
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input eventEmitInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		reg := modCtx.EventRegistry()
		if reg == nil || !reg.ModuleEmits(modCtx.ModuleName, input.Name) {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeUndeclared, Message: "event " + input.Name + " is not in this module's declared emits list"})
		}

		eventID := deriveEventID(reg, modCtx.ModuleName, modCtx.TenantID, input.Name, input.Payload, input.IdempotencyKey)
		emittedAt := time.Now()

		syncDispatched := false
		if input.Sync {
			if r.syncEventDispatcher == nil {
				return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
					Code:    abi.ErrCodeUnavailable,
					Message: "synchronous event dispatch is not available yet (engine still starting up)",
					Retry:   true,
				})
			}
			envelope, err := event.Envelope{
				ID: eventID.String(), Name: input.Name, Version: input.Version,
				EmitterModule: modCtx.ModuleName, TenantID: modCtx.TenantID, UserID: modCtx.UserID,
				TraceID: modCtx.TraceID, EmittedAt: emittedAt, Payload: input.Payload,
			}.Marshal()
			if err != nil {
				return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
			}
			if dispatchErr := dispatchSyncSubscribers(ctx, r.syncEventDispatcher, reg, input.Name, envelope, r.syncSubscriberTimeout); dispatchErr != nil {
				return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeDispatchFailed, Message: dispatchErr.Error()})
			}
			syncDispatched = true
		}

		// Set unconditionally, matching emit_tx's own convention — ByArgs
		// hashes only EventID (the sole river:"unique"-tagged field), so
		// this is a no-op when eventID is a fresh random UUIDv7 (no real
		// idempotency key involved) and real dedup only when eventID was
		// deterministically derived from one.
		uniqueOpts := &river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: 24 * time.Hour,
			ByState:  jobqueue.UniqueAcrossAllJobStates,
		}

		if err := insertEventDelivery(ctx, insertClient, eventID, input.Name, input.Version,
			modCtx.ModuleName, modCtx.TenantID, modCtx.UserID, modCtx.TraceID, input.Payload,
			time.Duration(input.DelayMs)*time.Millisecond, emittedAt, syncDispatched, uniqueOpts); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true})
		}

		return abi.WriteToModule(ctx, m, allocate, eventEmitOutput{EventID: eventID.String()})
	}
}
