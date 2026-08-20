// Package events implements the module-author-facing receiving side of
// goerp's event system: the Event type handed to a handler registered via
// engine.OnEvent, and the return-value types a handler uses to control
// retry/DLQ behavior (go-sdk-reference.md §7).
package events

import (
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

// Event is delivered to a handler registered via engine.OnEvent.
type Event struct {
	ID        string
	Name      string
	Version   int
	EmitterID string
	TenantID  string
	UserID    string
	TraceID   string
	EmittedAt time.Time

	payload []byte
}

// RawPayload returns the event's undecoded payload bytes.
func (e *Event) RawPayload() []byte { return e.payload }

// ParsePayload unmarshals the event's payload into dst.
func (e *Event) ParsePayload(dst any) error {
	return msgpack.Unmarshal(e.payload, dst)
}
