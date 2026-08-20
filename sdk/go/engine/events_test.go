package engine

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/events"
)

func withFreshEventHandlers(t *testing.T) {
	t.Helper()
	orig := eventHandlers
	eventHandlers = map[string]func(*events.Event) error{}
	t.Cleanup(func() { eventHandlers = orig })
}

func TestOnEvent_RegistersHandler(t *testing.T) {
	withFreshEventHandlers(t)

	called := false
	OnEvent("sale.order.confirmed", func(evt *events.Event) error {
		called = true
		return nil
	})

	handler, ok := eventHandlers["sale.order.confirmed"]
	if !ok {
		t.Fatal("handler not registered under \"sale.order.confirmed\"")
	}
	if err := handler(&events.Event{}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !called {
		t.Fatal("registered handler was not the one invoked")
	}
}

func TestOnEvent_SecondRegistrationOverwritesSameName(t *testing.T) {
	withFreshEventHandlers(t)

	OnEvent("sale.order.confirmed", func(evt *events.Event) error { return nil })
	OnEvent("sale.order.confirmed", func(evt *events.Event) error { return events.PermanentError(nil) })

	if len(eventHandlers) != 1 {
		t.Fatalf("got %d handlers, want 1", len(eventHandlers))
	}
}

func TestOnEvent_DifferentNamesBothRegister(t *testing.T) {
	withFreshEventHandlers(t)

	OnEvent("sale.order.confirmed", func(evt *events.Event) error { return nil })
	OnEvent("sale.order.cancelled", func(evt *events.Event) error { return nil })

	if len(eventHandlers) != 2 {
		t.Fatalf("got %d handlers, want 2", len(eventHandlers))
	}
}
