package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/events"
)

func writeWireEvent(t *testing.T, wire wireEvent) (ptr, length uint32) {
	t.Helper()
	data, err := marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire event: %v", err)
	}
	ptr = Allocate(uint32(len(data)))
	WriteMem(ptr, data)
	return ptr, uint32(len(data))
}

func TestDispatchEvent_SuccessReturnsZero(t *testing.T) {
	withFreshEventHandlers(t)

	var got *events.Event
	OnEvent("sale.order.confirmed", func(evt *events.Event) error {
		got = evt
		return nil
	})

	ptr, length := writeWireEvent(t, wireEvent{
		ID: "evt_1", Name: "sale.order.confirmed", Version: 2,
		EmitterModule: "sales", TenantID: "tenant_1", TraceID: "trace_1",
		EmittedAt: time.Unix(1000, 0).UTC(), Payload: []byte("payload"),
	})

	status := DispatchEvent(ptr, length)
	if status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if got == nil {
		t.Fatal("handler was not invoked")
	}
	if got.ID != "evt_1" || got.Name != "sale.order.confirmed" || got.Version != 2 || got.EmitterID != "sales" || got.TenantID != "tenant_1" {
		t.Errorf("unexpected event fields: %+v", got)
	}
	if string(got.RawPayload()) != "payload" {
		t.Errorf("RawPayload() = %q, want %q", got.RawPayload(), "payload")
	}
}

func TestDispatchEvent_RoutesByName(t *testing.T) {
	withFreshEventHandlers(t)

	var calledA, calledB bool
	OnEvent("sale.order.confirmed", func(evt *events.Event) error { calledA = true; return nil })
	OnEvent("sale.order.cancelled", func(evt *events.Event) error { calledB = true; return nil })

	ptr, length := writeWireEvent(t, wireEvent{Name: "sale.order.cancelled"})
	if status := DispatchEvent(ptr, length); status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if calledA {
		t.Error("wrong handler invoked: sale.order.confirmed's handler ran")
	}
	if !calledB {
		t.Error("sale.order.cancelled's own handler was not invoked")
	}
}

func TestDispatchEvent_UnregisteredNameReturnsRetryable(t *testing.T) {
	withFreshEventHandlers(t)

	ptr, length := writeWireEvent(t, wireEvent{Name: "does.not.exist"})
	if status := DispatchEvent(ptr, length); status != 1 {
		t.Fatalf("status = %d, want 1 (retryable)", status)
	}
}

func TestDispatchEvent_MalformedInputReturnsRetryable(t *testing.T) {
	withFreshEventHandlers(t)

	ptr := Allocate(4)
	WriteMem(ptr, []byte{0xFF, 0xFF, 0xFF, 0xFF}) // not valid msgpack for this shape

	if status := DispatchEvent(ptr, 4); status != 1 {
		t.Fatalf("status = %d, want 1 (retryable)", status)
	}
}

func TestDispatchEvent_PlainErrorReturnsRetryable(t *testing.T) {
	withFreshEventHandlers(t)

	OnEvent("sale.order.confirmed", func(evt *events.Event) error {
		return errors.New("transient db error")
	})

	ptr, length := writeWireEvent(t, wireEvent{Name: "sale.order.confirmed"})
	if status := DispatchEvent(ptr, length); status != 1 {
		t.Fatalf("status = %d, want 1 (retryable)", status)
	}
}

func TestDispatchEvent_PermanentErrorReturnsPermanent(t *testing.T) {
	withFreshEventHandlers(t)

	OnEvent("sale.order.confirmed", func(evt *events.Event) error {
		return events.PermanentError(errors.New("malformed payload"))
	})

	ptr, length := writeWireEvent(t, wireEvent{Name: "sale.order.confirmed"})
	if status := DispatchEvent(ptr, length); status != 2 {
		t.Fatalf("status = %d, want 2 (permanent)", status)
	}
}

func TestDispatchEvent_RetryAfterDegradesToRetryable(t *testing.T) {
	withFreshEventHandlers(t)

	OnEvent("sale.order.confirmed", func(evt *events.Event) error {
		return events.RetryAfter(time.Hour, errors.New("rate limited"))
	})

	ptr, length := writeWireEvent(t, wireEvent{Name: "sale.order.confirmed"})
	if status := DispatchEvent(ptr, length); status != 1 {
		t.Fatalf("status = %d, want 1 (RetryAfter degrades to ordinary retryable, its custom delay is not honored by this ABI)", status)
	}
}
