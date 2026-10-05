// Package events implements the module-author-facing side of goerp's
// event system: typed event definitions and emitting (def.go, emit.go),
// the Event[P] envelope handed to a handler registered via
// engine.Subscribe, and the retry and return-value types a subscription
// uses to control retry/DLQ behavior (go-sdk-reference.md §7).
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
