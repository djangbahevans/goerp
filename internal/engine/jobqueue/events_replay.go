package jobqueue

import (
	"time"

	"github.com/riverqueue/river"
)

// EventsReplayArgs is the one admin job POST /admin/events/replay inserts
// per non-dry-run request (cli-reference.md §8 "goerp events replay",
// event-system.md §10 "Event replay") — processed by
// internal/engine/eventdelivery.EventsReplayWorker, which pages through
// the matching event_log rows and enqueues one SubscriberDeliveryArgs job
// per matched-event/targeted-subscriber pair directly, bypassing fan-out
// entirely. Runs on the bulk queue so a large replay never starves
// interactive admin operations.
type EventsReplayArgs struct {
	Tenant      string    `json:"tenant"` // slug, or "all"
	EventNames  []string  `json:"event_names"`
	Module      string    `json:"module,omitempty"`
	Subscribers []string  `json:"subscribers,omitempty"` // module names; empty = every current async subscriber
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	BatchSize   int       `json:"batch_size"`
}

func (EventsReplayArgs) Kind() string { return "events_replay" }

func (EventsReplayArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueBulk}
}
