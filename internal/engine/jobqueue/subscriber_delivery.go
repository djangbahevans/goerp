package jobqueue

import (
	"time"

	"github.com/riverqueue/river"
)

// SubscriberDeliveryArgs is the River job EventDeliveryWorker inserts once
// per async subscriber of a dispatched event (engine-internals.md §9
// "Event delivery worker"/"Subscriber delivery worker"), and processed by
// eventdelivery.SubscriberDeliveryWorker (goerp#129) — invoking
// ModuleName/HandlerName's WASM handle_event export via a
// event.Envelope built from these fields.
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
}

func (SubscriberDeliveryArgs) Kind() string { return "subscriber_delivery" }

func (SubscriberDeliveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEvents}
}
