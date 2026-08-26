package wasm

import (
	"context"
	"database/sql"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// insertEventDeliveryTx inserts one EventDelivery job on tx via
// insertClient — the shared core both host.event.emit_tx
// (module-facing, gated on the emitting module's own declared emits
// list, deduped by an optional caller-supplied idempotency key) and the
// engine-native host.orm write pipeline (goerp#343's orm.record.*
// events, which skip that gate entirely — they're engine-emitted, not
// module-declared, the same reasoning event-system.md gives system.*
// events) call. uniqueOpts is nil to skip River's dedup entirely, which
// the engine-native path always does: every write is already its own
// distinct transaction, with no caller-supplied idempotency key to dedup
// against.
func insertEventDeliveryTx(
	ctx context.Context,
	insertClient *river.Client[*sql.Tx],
	tx *sql.Tx,
	eventID uuid.UUID,
	name string,
	version int,
	emitterModule, tenantID, userID, traceID string,
	payload []byte,
	delay time.Duration,
	uniqueOpts *river.UniqueOpts,
) error {
	emittedAt := time.Now()
	opts := &river.InsertOpts{
		Queue:       jobqueue.QueueEvents,
		Priority:    1,
		ScheduledAt: emittedAt.Add(delay),
	}
	if uniqueOpts != nil {
		opts.UniqueOpts = *uniqueOpts
	}

	_, err := insertClient.InsertTx(ctx, tx, &jobqueue.EventDeliveryArgs{
		EventID:       eventID.String(),
		EventName:     name,
		EventVersion:  version,
		EmitterModule: emitterModule,
		TenantID:      tenantID,
		UserID:        userID,
		TraceID:       traceID,
		Payload:       payload,
		EmittedAt:     emittedAt,
	}, opts)
	return err
}

// insertEventDelivery is insertEventDeliveryTx's non-transactional
// counterpart, for host.event.emit (goerp#129) — the only emit path that
// can honor events.WithSync(), since a still-open transaction can never
// wait on an inline synchronous dispatch (event-system.md §8: "An event
// with synchronous subscribers can only ever be emitted through
// non-transactional Emit, never EmitTx"). syncDispatched records whether
// inline dispatch already ran for this emission's async:false
// subscribers, so eventdelivery.Worker's own fan-out knows which of them
// still need an async fallback delivery.
func insertEventDelivery(
	ctx context.Context,
	insertClient *river.Client[*sql.Tx],
	eventID uuid.UUID,
	name string,
	version int,
	emitterModule, tenantID, userID, traceID string,
	payload []byte,
	delay time.Duration,
	emittedAt time.Time,
	syncDispatched bool,
	uniqueOpts *river.UniqueOpts,
) error {
	opts := &river.InsertOpts{
		Queue:       jobqueue.QueueEvents,
		Priority:    1,
		ScheduledAt: emittedAt.Add(delay),
	}
	if uniqueOpts != nil {
		opts.UniqueOpts = *uniqueOpts
	}

	_, err := insertClient.Insert(ctx, &jobqueue.EventDeliveryArgs{
		EventID:        eventID.String(),
		EventName:      name,
		EventVersion:   version,
		EmitterModule:  emitterModule,
		TenantID:       tenantID,
		UserID:         userID,
		TraceID:        traceID,
		Payload:        payload,
		EmittedAt:      emittedAt,
		SyncDispatched: syncDispatched,
	}, opts)
	return err
}
