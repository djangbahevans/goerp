package wasm

import (
	"context"
	"database/sql"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
)

// insertEventDeliveryTx shares a transaction with the emitting write. Nil uniqueOpts
// disables River deduplication for engine-generated record events.
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
		Transactional: true,
	}, opts)
	return err
}

// insertEventDelivery supports non-transactional synchronous dispatch. syncDispatched
// prevents the delivery worker from invoking inline subscribers a second time.
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
