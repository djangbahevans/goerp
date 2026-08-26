package jobqueue

import (
	"time"

	"github.com/riverqueue/river"
)

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
	// EmittedAt is captured once, at the moment the event is
	// transactionally inserted (insertEventDeliveryTx), and carried
	// through to eventdelivery.Worker's event_log write. It must stay
	// stable across a River job retry — event_log's PK is composite
	// (id, emitted_at) (goerp#194, table partitioned by emitted_at), so a
	// value recomputed fresh on each retry would defeat the worker's own
	// "ON CONFLICT (id, emitted_at) DO NOTHING" idempotency and insert a
	// duplicate row instead of deduping.
	EmittedAt time.Time `json:"emitted_at"`
	// SyncDispatched is set true only when this emission requested inline
	// synchronous dispatch (events.WithSync(), goerp#129) and that inline
	// dispatch actually ran. eventdelivery.Worker's fan-out uses it to
	// decide, per async:false subscriber, whether to skip it (already
	// handled inline) or fall it through to an ordinary async insert —
	// the documented fallback for a plain Emit/EmitTx that never
	// requested sync dispatch at all (event-system.md §8). Deliberately
	// excluded from the EventID uniqueness key's discriminating fields:
	// it describes how this emission was dispatched, not what event it
	// is, and must never affect whether two emissions of the same event
	// dedupe against each other.
	SyncDispatched bool `json:"sync_dispatched,omitempty"`
}

func (EventDeliveryArgs) Kind() string { return "event_delivery" }

func (EventDeliveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEvents}
}
