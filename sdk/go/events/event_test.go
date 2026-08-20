package events

import (
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

type testPayload struct {
	OrderID string `msgpack:"order_id"`
	Total   int    `msgpack:"total"`
}

func TestEvent_ParsePayload(t *testing.T) {
	raw, err := msgpack.Marshal(testPayload{OrderID: "ord_1", Total: 4200})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	evt := &Event{
		ID:        "evt_1",
		Name:      "sale.order.confirmed",
		Version:   1,
		EmitterID: "sales",
		TenantID:  "tenant_1",
		UserID:    "user_1",
		TraceID:   "trace_1",
		EmittedAt: time.Now(),
		payload:   raw,
	}

	var got testPayload
	if err := evt.ParsePayload(&got); err != nil {
		t.Fatalf("ParsePayload: %v", err)
	}
	if got != (testPayload{OrderID: "ord_1", Total: 4200}) {
		t.Fatalf("got %+v, want {OrderID:ord_1 Total:4200}", got)
	}
}

func TestEvent_ParsePayload_MalformedReturnsError(t *testing.T) {
	evt := &Event{payload: []byte("not valid msgpack")}

	var got testPayload
	if err := evt.ParsePayload(&got); err == nil {
		t.Fatal("want error for malformed payload, got nil")
	}
}

func TestEvent_RawPayload(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03}
	evt := &Event{payload: raw}

	got := evt.RawPayload()
	if len(got) != len(raw) {
		t.Fatalf("RawPayload() = %v, want %v", got, raw)
	}
	for i := range raw {
		if got[i] != raw[i] {
			t.Fatalf("RawPayload() = %v, want %v", got, raw)
		}
	}
}
