package engine

import (
	"errors"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/events"
)

func writeWireEvent(t *testing.T, wire abi.EventEnvelope) (ptr, length uint32) {
	t.Helper()
	data, err := marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire event: %v", err)
	}
	ptr = Allocate(uint32(len(data)))
	WriteMem(ptr, data)
	return ptr, uint32(len(data))
}

func orderBytes(t *testing.T, id string) []byte {
	t.Helper()
	data, err := marshal(orderPayload{OrderID: id})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

func subscribeConfirmed(version int, handler func(events.Event[orderPayload]) error) {
	Subscribe(events.Define[orderPayload]("sale.order.confirmed", events.Version(version)), handler)
}

func TestDispatchEvent_SuccessDecodesPayloadAndEnvelope(t *testing.T) {
	withFreshSubscriptions(t)

	var got events.Event[orderPayload]
	subscribeConfirmed(2, func(evt events.Event[orderPayload]) error {
		got = evt
		return nil
	})

	ptr, length := writeWireEvent(t, abi.EventEnvelope{
		ID: "evt_1", Name: "sale.order.confirmed", Version: 2,
		EmitterModule: "sales", TenantID: "tenant_1", TraceID: "trace_1",
		EmittedAt: time.Unix(1000, 0).UTC(), Payload: orderBytes(t, "ord_1"),
	})

	if status := DispatchEvent(ptr, length); status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if got.ID != "evt_1" || got.Name != "sale.order.confirmed" || got.Version != 2 || got.EmitterID != "sales" || got.TenantID != "tenant_1" {
		t.Errorf("unexpected event fields: %+v", got)
	}
	if got.Payload.OrderID != "ord_1" {
		t.Errorf("Payload = %+v, want OrderID ord_1", got.Payload)
	}
}

func TestDispatchEvent_RoutesByNameAndVersion(t *testing.T) {
	withFreshSubscriptions(t)

	var calledV1, calledV2, calledOther bool
	subscribeConfirmed(1, func(events.Event[orderPayload]) error { calledV1 = true; return nil })
	subscribeConfirmed(2, func(events.Event[orderPayload]) error { calledV2 = true; return nil })
	Subscribe(events.Define[orderPayload]("sale.order.cancelled"), func(events.Event[orderPayload]) error { calledOther = true; return nil })

	ptr, length := writeWireEvent(t, abi.EventEnvelope{Name: "sale.order.confirmed", Version: 2, Payload: orderBytes(t, "x")})
	if status := DispatchEvent(ptr, length); status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if calledV1 || calledOther || !calledV2 {
		t.Errorf("calledV1=%v calledV2=%v calledOther=%v, want only v2", calledV1, calledV2, calledOther)
	}
}

func TestDispatchEvent_UnregisteredVersionReturnsRetryable(t *testing.T) {
	withFreshSubscriptions(t)

	called := false
	subscribeConfirmed(1, func(events.Event[orderPayload]) error { called = true; return nil })

	ptr, length := writeWireEvent(t, abi.EventEnvelope{Name: "sale.order.confirmed", Version: 3, Payload: orderBytes(t, "x")})
	if status := DispatchEvent(ptr, length); status != 1 {
		t.Fatalf("status = %d, want 1 (retryable)", status)
	}
	if called {
		t.Error("v1 handler ran for a v3 delivery")
	}
}

func TestDispatchEvent_UnregisteredNameReturnsRetryable(t *testing.T) {
	withFreshSubscriptions(t)

	ptr, length := writeWireEvent(t, abi.EventEnvelope{Name: "does.not.exist", Version: 1})
	if status := DispatchEvent(ptr, length); status != 1 {
		t.Fatalf("status = %d, want 1 (retryable)", status)
	}
}

func TestDispatchEvent_MalformedPayloadReturnsPermanentWithoutCallingHandler(t *testing.T) {
	withFreshSubscriptions(t)

	called := false
	subscribeConfirmed(1, func(events.Event[orderPayload]) error { called = true; return nil })

	ptr, length := writeWireEvent(t, abi.EventEnvelope{Name: "sale.order.confirmed", Version: 1, Payload: []byte{0xc1}})
	if status := DispatchEvent(ptr, length); status != 2 {
		t.Fatalf("status = %d, want 2 (permanent)", status)
	}
	if called {
		t.Error("handler ran for a payload that failed to decode")
	}
}

func TestDispatchEvent_MalformedInputReturnsRetryable(t *testing.T) {
	withFreshSubscriptions(t)

	ptr := Allocate(4)
	WriteMem(ptr, []byte{0xFF, 0xFF, 0xFF, 0xFF}) // not valid msgpack for this shape

	if status := DispatchEvent(ptr, 4); status != 1 {
		t.Fatalf("status = %d, want 1 (retryable)", status)
	}
}

func TestDispatchEvent_PlainErrorReturnsRetryable(t *testing.T) {
	withFreshSubscriptions(t)

	subscribeConfirmed(1, func(events.Event[orderPayload]) error {
		return errors.New("transient db error")
	})

	ptr, length := writeWireEvent(t, abi.EventEnvelope{Name: "sale.order.confirmed", Version: 1, Payload: orderBytes(t, "x")})
	if status := DispatchEvent(ptr, length); status != 1 {
		t.Fatalf("status = %d, want 1 (retryable)", status)
	}
}

func TestDispatchEvent_PermanentErrorReturnsPermanent(t *testing.T) {
	withFreshSubscriptions(t)

	subscribeConfirmed(1, func(events.Event[orderPayload]) error {
		return events.PermanentError(errors.New("malformed payload"))
	})

	ptr, length := writeWireEvent(t, abi.EventEnvelope{Name: "sale.order.confirmed", Version: 1, Payload: orderBytes(t, "x")})
	if status := DispatchEvent(ptr, length); status != 2 {
		t.Fatalf("status = %d, want 2 (permanent)", status)
	}
}

func TestDispatchEvent_RetryAfterDegradesToRetryable(t *testing.T) {
	withFreshSubscriptions(t)

	subscribeConfirmed(1, func(events.Event[orderPayload]) error {
		return events.RetryAfter(time.Hour, errors.New("rate limited"))
	})

	ptr, length := writeWireEvent(t, abi.EventEnvelope{Name: "sale.order.confirmed", Version: 1, Payload: orderBytes(t, "x")})
	if status := DispatchEvent(ptr, length); status != 1 {
		t.Fatalf("status = %d, want 1 (RetryAfter degrades to ordinary retryable, its custom delay is not honored by this ABI)", status)
	}
}
