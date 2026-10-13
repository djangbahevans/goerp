package engine

import "github.com/djangbahevans/goerp/sdk/go/declare"

func init() { declare.ValidateWith(ValidateRoutes) }

// Event carries JSON data and an optional cursor for the next poll.
// IDs must not contain CR, LF or NUL.
// A batch contains at most 100 events, each with at most 64 KiB of encoded data.
type Event struct {
	ID   string `json:"id,omitempty"`
	Data any    `json:"data"`
}

type eventBatch struct {
	Events []Event `json:"events"`
	Done   bool    `json:"done"`
}

// Events returns this poll's batch and keeps the event stream open.
// An empty batch is valid.
func Events(events ...Event) *Response {
	return OK(eventBatch{Events: events})
}

// EventsDone delivers the final batch, sends a done event and closes the stream.
func EventsDone(events ...Event) *Response {
	return OK(eventBatch{Events: events, Done: true})
}
