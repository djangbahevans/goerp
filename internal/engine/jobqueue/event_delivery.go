package jobqueue

import "github.com/riverqueue/river"

// EventDeliveryArgs is the River job host.event.emit_tx inserts
// transactionally (engine-internals.md §9) — the record of a domain
// event, dispatched to subscribers only once the inserting transaction
// commits. No Worker is registered for this kind yet: the fan-out-to-
// subscribers step (EventDeliveryWorker, engine-internals.md §9 "Event
// delivery worker") is separate, unfiled work. Until it exists, an
// inserted job sits in the "events" queue, gets fetched by the started
// client's producer, fails with river.UnknownJobKindError, and is
// retried per River's default retry policy before eventually landing in
// "discarded" — a known, temporary, non-fatal state (per-job failures,
// not a process-level outage), not something this ticket's own scope
// (emission, not dispatch) needs to solve.
type EventDeliveryArgs struct {
	// EventID is river:"unique"-tagged alone — the deterministic UUID
	// derivation (engine-internals.md §9) already encodes tenant/event
	// name/idempotency key, so it's the sole discriminator River's
	// ByArgs uniqueness check needs.
	EventID       string `json:"event_id" river:"unique"`
	EventName     string `json:"event_name"`
	EventVersion  int    `json:"event_version"`
	EmitterModule string `json:"emitter_module"`
	TenantID      string `json:"tenant_id"`
	UserID        string `json:"user_id"`
	TraceID       string `json:"trace_id"`
	Payload       []byte `json:"payload"`
}

func (EventDeliveryArgs) Kind() string { return "event_delivery" }

func (EventDeliveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEvents}
}
