package wasm

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"fmt"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/riverqueue/river"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// The namespace must stay fixed so retries retain their identity throughout
// the 24-hour deduplication window.
var idempotencyKeyNamespace = uuid.MustParse("6f8c2b1e-6b8b-4f0a-9b1a-9c9e6a2d3b7c")

// The insert-only SQL client shares host.db transactions; River workers use
// the same job table through the engine's pgx client.
func registerHostEvent(ctx context.Context, rt wazero.Runtime, r *Runtime, insertClient *river.Client[*sql.Tx]) error {
	_, err := r.guardedHostModule(rt, "host.event").
		NewFunctionBuilder().WithFunc(makeEventEmitTx(r, insertClient)).Export("emit_tx").
		NewFunctionBuilder().WithFunc(makeEventEmit(r, insertClient)).Export("emit").
		Instantiate(ctx)
	return err
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
		var input abiv1.EventEmitTxInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}
		if input.Version <= 0 {
			input.Version = manifest.DefaultEventVersion
		}

		if input.Sync {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeSyncNotAllowed,
				Message: "synchronous dispatch is not permitted with emit_tx — use host.event.emit instead",
			})
		}

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeNoTransaction, Message: "tx_id does not exist or has expired"})
		}

		reg := modCtx.EventRegistry()
		if reg == nil || !reg.ModuleEmits(modCtx.ModuleName, input.Name, input.Version) {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUndeclared,
				Message: fmt.Sprintf("event %s v%d is not in this module's declared emits list", input.Name, input.Version),
			})
		}

		if err := event.ValidatePayload(input.Payload); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, err)
		}

		eventID := deriveEventID(reg, modCtx.ModuleName, modCtx.TenantID, input.Name, input.Payload, input.IdempotencyKey)

		err = insertEventDeliveryTx(ctx, insertClient, tx, eventID, input.Name, input.Version,
			modCtx.ModuleName, modCtx.TenantID, modCtx.UserID, modCtx.TraceID, input.Payload,
			time.Duration(input.DelayMs)*time.Millisecond,
			&river.UniqueOpts{
				// Retain deduplication after completion, bounded to 24 hours so
				// legitimately reused keys can identify new events later.
				ByArgs:   true,
				ByPeriod: 24 * time.Hour,
				ByState:  jobqueue.UniqueAcrossAllJobStates,
			})
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true})
		}

		return abi.WriteToModule(ctx, m, allocate, abiv1.EventEmitTxOutput{EventID: eventID.String()})
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

func deriveEventID(reg *event.EventRegistry, moduleName, tenantID, name string, payload []byte, explicitKey string) uuid.UUID {
	idempotencyKey := explicitKey
	if idempotencyKey == "" {
		if field, ok := reg.IdempotencyKeyField(moduleName, name); ok {
			idempotencyKey, _ = extractPayloadField(payload, field)
		}
	}
	if idempotencyKey == "" {
		return uuid.NewV7()
	}
	return newSHA1UUID(idempotencyKeyNamespace, []byte(tenantID+"|"+name+"|"+idempotencyKey))
}

// newSHA1UUID returns the name-based version 5 UUID for name in namespace
// (RFC 9562 §5.5), which the standard library uuid package does not provide.
func newSHA1UUID(namespace uuid.UUID, name []byte) uuid.UUID {
	h := sha1.New()
	h.Write(namespace[:])
	h.Write(name)
	var u uuid.UUID
	copy(u[:], h.Sum(nil))
	u[6] = u[6]&0x0f | 0x50
	u[8] = u[8]&0x3f | 0x80
	return u
}

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
		var input abiv1.EventEmitInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}
		if input.Version <= 0 {
			input.Version = manifest.DefaultEventVersion
		}

		reg := modCtx.EventRegistry()
		if reg == nil || !reg.ModuleEmits(modCtx.ModuleName, input.Name, input.Version) {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUndeclared,
				Message: fmt.Sprintf("event %s v%d is not in this module's declared emits list", input.Name, input.Version),
			})
		}

		if err := event.ValidatePayload(input.Payload); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, err)
		}

		eventID := deriveEventID(reg, modCtx.ModuleName, modCtx.TenantID, input.Name, input.Payload, input.IdempotencyKey)
		emittedAt := time.Now()

		syncDispatched := false
		if input.Sync {
			if r.syncEventDispatcher == nil {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
					Code:    abiv1.ErrCodeUnavailable,
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
			if dispatchErr := dispatchSyncSubscribers(ctx, r.syncEventDispatcher, reg, input.Name, input.Version, envelope, r.syncSubscriberTimeout); dispatchErr != nil {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeDispatchFailed, Message: dispatchErr.Error()})
			}
			syncDispatched = true
		}

		uniqueOpts := &river.UniqueOpts{
			ByArgs:   true,
			ByPeriod: 24 * time.Hour,
			ByState:  jobqueue.UniqueAcrossAllJobStates,
		}

		if err := insertEventDelivery(ctx, insertClient, eventID, input.Name, input.Version,
			modCtx.ModuleName, modCtx.TenantID, modCtx.UserID, modCtx.TraceID, input.Payload,
			time.Duration(input.DelayMs)*time.Millisecond, emittedAt, syncDispatched, uniqueOpts); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true})
		}

		return abi.WriteToModule(ctx, m, allocate, abiv1.EventEmitOutput{EventID: eventID.String()})
	}
}
