package jobqueue

import "github.com/riverqueue/river"

// EventDeliveryArgs is the River job host.event.emit_tx inserts
// transactionally (engine-internals.md §9) — the record of a domain
// event, dispatched to subscribers only once the inserting transaction
// commits. Processed by eventdelivery.Worker (internal/engine/
// eventdelivery, goerp#16 — lives outside this package to avoid an
// import cycle, see that package's own doc comment), which fans the
// event out to its async subscribers as SubscriberDeliveryArgs jobs and
// writes the event_log audit row.
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
