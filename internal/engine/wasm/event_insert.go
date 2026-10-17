package wasm

import (
	"context"
	"database/sql"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
	"go.opentelemetry.io/otel/trace"
)

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
) (err error) {
	ctx, span := event.StartEmission(ctx, name, version, emitterModule)
	defer func() { event.FinishSpan(span, err) }()

	return enqueueEventDeliveryTx(ctx, insertClient, tx, eventID, name, version,
		emitterModule, tenantID, userID, traceID, payload, delay, uniqueOpts)
}

// enqueueEventDeliveryTx shares a transaction with the emitting write. Nil uniqueOpts
// disables River deduplication for engine-generated record events.
func enqueueEventDeliveryTx(
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
	if err := event.ValidatePayload(payload); err != nil {
		return err
	}

	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		traceID = spanContext.TraceID().String()
	}

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
		SpanID:        spanIDString(spanContext),
		TraceFlags:    uint8(spanContext.TraceFlags()),
		Payload:       payload,
		EmittedAt:     emittedAt,
		Transactional: true,
	}, opts)
	return err
}

// syncDispatched prevents queued fan-out from invoking subscribers already called inline.
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
	if err := event.ValidatePayload(payload); err != nil {
		return err
	}

	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		traceID = spanContext.TraceID().String()
	}

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
		SpanID:         spanIDString(spanContext),
		TraceFlags:     uint8(spanContext.TraceFlags()),
		Payload:        payload,
		EmittedAt:      emittedAt,
		SyncDispatched: syncDispatched,
	}, opts)
	return err
}

func spanIDString(sc trace.SpanContext) string {
	if !sc.IsValid() {
		return ""
	}
	return sc.SpanID().String()
}
