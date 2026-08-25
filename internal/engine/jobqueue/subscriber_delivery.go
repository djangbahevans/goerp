package jobqueue

import "github.com/riverqueue/river"

// SubscriberDeliveryArgs is the River job EventDeliveryWorker inserts once
// per async subscriber of a dispatched event (engine-internals.md §9
// "Event delivery worker"/"Subscriber delivery worker"). Deliberately
// narrower than EventDeliveryArgs — EventVersion, EmitterModule, and
// UserID exist there but not here, matching the doc's own two
// construction sites for this shape exactly. No Worker is registered for
// this kind yet: subscriber *execution* — actually invoking
// ModuleName/HandlerName's WASM handler — is separate, unfiled work
// (engine-internals.md §9 "Subscriber delivery worker"), the same way
// EventDeliveryArgs itself shipped without a worker under goerp#341
// before EventDeliveryWorker (goerp#16) came along to process it. Until
// a worker exists, an inserted job sits in the "events" queue and is
// eventually discarded the same way EventDeliveryArgs jobs were before
// #16 — a known, temporary, non-fatal state.
type SubscriberDeliveryArgs struct {
	EventID     string `json:"event_id"`
	EventName   string `json:"event_name"`
	ModuleName  string `json:"module_name"`
	HandlerName string `json:"handler_name"`
	Payload     []byte `json:"payload"`
	TenantID    string `json:"tenant_id"`
	TraceID     string `json:"trace_id"`
}

func (SubscriberDeliveryArgs) Kind() string { return "subscriber_delivery" }

func (SubscriberDeliveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEvents}
}
