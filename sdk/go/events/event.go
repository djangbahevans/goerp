// Package events defines and emits typed events, and provides the Event[P]
// envelope a handler registered via engine.Subscribe receives, plus the
// return values a subscription uses to control retries and dead-lettering.
package events

import "time"

// Event is delivered to a handler registered via engine.Subscribe,
// carrying the envelope and the payload decoded into P.
type Event[P any] struct {
	ID        string
	Name      string
	Version   int
	EmitterID string
	TenantID  string
	UserID    string
	TraceID   string
	EmittedAt time.Time
	Payload   P
}
