package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
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

// registerHostEvent attaches host.event.emit_tx to the runtime. Lives in
// the wasm package for the same import-cycle reason registerHostDB does —
// its closure needs direct access to the Runtime's instance registry and
// an insert-capable River client.
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

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeNoTransaction, Message: "tx_id does not exist or has expired"})
		}

		reg := modCtx.EventRegistry()
		if reg == nil || !reg.ModuleEmits(modCtx.ModuleName, input.Name) {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeUndeclared, Message: "event " + input.Name + " is not in this module's declared emits list"})
		}

		idempotencyKey := input.IdempotencyKey
		if idempotencyKey == "" {
			if field, ok := reg.IdempotencyKeyField(modCtx.ModuleName, input.Name); ok {
				idempotencyKey, _ = extractPayloadField(input.Payload, field)
			}
		}

		eventID := uuid.Must(uuid.NewV7())
		if idempotencyKey != "" {
			eventID = uuid.NewSHA1(idempotencyKeyNamespace, []byte(modCtx.TenantID+"|"+input.Name+"|"+idempotencyKey))
		}

		_, err = insertClient.InsertTx(ctx, tx, &jobqueue.EventDeliveryArgs{
			EventID:       eventID.String(),
			EventName:     input.Name,
			EventVersion:  input.Version,
			EmitterModule: modCtx.ModuleName,
			TenantID:      modCtx.TenantID,
			UserID:        modCtx.UserID,
			TraceID:       modCtx.TraceID,
			Payload:       input.Payload,
		}, &river.InsertOpts{
			Queue:       jobqueue.QueueEvents,
			Priority:    1,
			ScheduledAt: time.Now().Add(time.Duration(input.DelayMs) * time.Millisecond),
			// Bounded to 24h, not unbounded, so a legitimately new event
			// that happens to reuse an idempotency key after a long gap
			// isn't silently dropped forever. ByState explicitly includes
			// terminal states — River's default ByState only counts
			// "active" jobs, which would let a retry arriving after the
			// original event already finished dispatching insert a
			// second, duplicate job, defeating the guarantee
			// idempotency_key exists to provide.
			UniqueOpts: river.UniqueOpts{
				ByArgs:   true,
				ByPeriod: 24 * time.Hour,
				// Available/Pending/Running/Scheduled are required by
				// river v0.43.0's own UniqueOpts.validate() — omitting any
				// of them is a hard insert-time error, not just a missed
				// dedup case. Retryable/Completed/Discarded are added on
				// top so a retry arriving after the original event
				// already finished dispatching (the common case, not an
				// edge case) still sees the prior job and dedupes against
				// it, rather than River's default ByState (which only
				// counts "active" jobs) letting a second one through.
				ByState: []rivertype.JobState{
					rivertype.JobStateAvailable, rivertype.JobStatePending,
					rivertype.JobStateRunning, rivertype.JobStateScheduled,
					rivertype.JobStateRetryable, rivertype.JobStateCompleted,
					rivertype.JobStateDiscarded,
				},
			},
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
