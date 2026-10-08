package jobqueue

import (
	"time"

	"github.com/riverqueue/river"
)

// SubscriberDeliveryArgs identifies one event subscriber invocation, carrying the envelope
// fields needed by its WASM handler.
type SubscriberDeliveryArgs struct {
	EventID       string    `json:"event_id"`
	EventName     string    `json:"event_name"`
	EventVersion  int       `json:"event_version"`
	EmitterModule string    `json:"emitter_module"`
	ModuleName    string    `json:"module_name"`
	HandlerName   string    `json:"handler_name"`
	Payload       []byte    `json:"payload"`
	TenantID      string    `json:"tenant_id"`
	UserID        string    `json:"user_id,omitempty"`
	TraceID       string    `json:"trace_id"`
	EmittedAt     time.Time `json:"emitted_at"`
	Replay        bool      `json:"replay,omitzero"`
}

func (SubscriberDeliveryArgs) Kind() string { return "subscriber_delivery" }

func (SubscriberDeliveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEvents}
}
