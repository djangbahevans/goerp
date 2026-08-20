package engine

import "github.com/djangbahevans/goerp/sdk/go/events"

var eventHandlers = map[string]func(*events.Event) error{}

// OnEvent registers a handler for a named event, called in init(). The
// engine-side dispatch layer that invokes a registered handler is
// separate, later work — this only records the (name, handler) pair.
func OnEvent(name string, handler func(*events.Event) error) {
	eventHandlers[name] = handler
}
