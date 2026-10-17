package event

import (
	"time"

	"github.com/riverqueue/river"
)

// EventDeliveryArgs records a domain event transactionally. After commit, the delivery
// worker fans out subscriber jobs and writes the event log.
type EventDeliveryArgs struct {
	// Deduplication uses EventID alone; payload and dispatch options can differ on retry.
	EventID       string `json:"event_id" river:"unique"`
	EventName     string `json:"event_name"`
	EventVersion  int    `json:"event_version"`
	EmitterModule string `json:"emitter_module"`
	TenantID      string `json:"tenant_id"`
	UserID        string `json:"user_id"`
	TraceID       string `json:"trace_id"`
	SpanID        string `json:"span_id,omitempty"`
	TraceFlags    uint8  `json:"trace_flags,omitzero"`
	Payload       []byte `json:"payload"`
	// EmittedAt stays fixed across retries because event_log deduplicates on the
	// partitioned primary key (id, emitted_at).
	EmittedAt time.Time `json:"emitted_at"`
	// SyncDispatched suppresses duplicate delivery to subscribers already invoked inline.
	// It is excluded from event uniqueness because dispatch mode does not change event
	// identity.
	SyncDispatched bool `json:"sync_dispatched,omitzero"`
	Transactional  bool `json:"transactional,omitzero"`
}

func (EventDeliveryArgs) Kind() string { return "event_delivery" }

func (EventDeliveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "events"}
}
