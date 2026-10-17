// Package events defines and emits typed events, and provides the Event[P]
// envelope a handler registered via engine.Subscribe receives, plus the
// return values a subscription uses to control retries and dead-lettering.
package events

import "time"

// Event carries delivery identity, resolved presentation settings and a payload
// decoded into P for a handler registered via engine.Subscribe.
type Event[P any] struct {
	Locale    string
	Timezone  string
	Currency  string
	Direction string
	ID        string
	Name      string
	Version   int
	EmitterID string
	TenantID  string
	UserID    string

	// TraceID and SpanID identify the emission; subscriber work has its own current span.
	TraceID    string
	SpanID     string
	TraceFlags uint8
	EmittedAt  time.Time
	Payload    P
}
