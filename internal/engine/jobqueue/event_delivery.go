package jobqueue

import (
	"time"

	"github.com/riverqueue/river"
)

// EventDeliveryArgs records a domain event transactionally. After commit, the delivery
// worker fans out subscriber jobs and writes the event log.
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
	// EmittedAt stays fixed across retries because event_log deduplicates on the
	// partitioned primary key (id, emitted_at).
	EmittedAt time.Time `json:"emitted_at"`
	// SyncDispatched suppresses duplicate delivery to subscribers already invoked inline.
	// It is excluded from event uniqueness because dispatch mode does not change event
	// identity.
	SyncDispatched bool `json:"sync_dispatched,omitzero"`
	// Transactional records whether this emission came in through
	// host.event.emit_tx (true) rather than host.event.emit (false) — the
	// two host functions share this args type and insertEventDeliveryTx/
	// insertEventDelivery respectively, but only the tx path's emission
	// happened inside, and only becomes visible on commit of, the
	// caller's own transaction. sdk/go/modeltest's h.Events exposes this
	// as Event.WasTransactional (testing-guide.md §8).
	Transactional bool `json:"transactional,omitzero"`
}

func (EventDeliveryArgs) Kind() string { return "event_delivery" }

func (EventDeliveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEvents}
}
