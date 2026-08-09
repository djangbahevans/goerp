package event

import (
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func TestEventRegistry_Register_Emitters(t *testing.T) {
	r := NewEventRegistry()

	r.Register("sales", manifest.Manifest{
		Emits: []manifest.EventDeclaration{
			{Name: "sale.order.confirmed"},
			{Name: "sale.order.cancelled"},
		},
	})

	if got := r.Emitters("sale.order.confirmed"); len(got) != 1 || got[0] != "sales" {
		t.Errorf("Emitters(sale.order.confirmed) = %v, want [sales]", got)
	}
	if got := r.Emitters("sale.order.unknown"); len(got) != 0 {
		t.Errorf("Emitters(sale.order.unknown) = %v, want empty", got)
	}
}

func TestEventRegistry_ModuleEmits(t *testing.T) {
	r := NewEventRegistry()

	r.Register("sales", manifest.Manifest{
		Emits: []manifest.EventDeclaration{
			{Name: "sale.order.confirmed"},
		},
		Subscribes: []manifest.EventSubscription{
			{Name: "inventory.move.validated", Handler: "handle_move_validated"},
		},
	})

	if !r.ModuleEmits("sales", "sale.order.confirmed") {
		t.Error("ModuleEmits(sales, sale.order.confirmed) = false, want true")
	}
	if r.ModuleEmits("sales", "inventory.move.validated") {
		t.Error("ModuleEmits(sales, inventory.move.validated) = true, want false — sales only subscribes to this, doesn't emit it")
	}
	if r.ModuleEmits("unknown_module", "sale.order.confirmed") {
		t.Error("ModuleEmits(unknown_module, ...) = true, want false")
	}
}

func TestEventRegistry_Register_SubscribersCarryAsyncFlag(t *testing.T) {
	r := NewEventRegistry()

	r.Register("inventory", manifest.Manifest{
		Subscribes: []manifest.EventSubscription{
			{Name: "sale.order.confirmed", Handler: "handle_confirmed_async", Async: true},
		},
	})
	r.Register("audit", manifest.Manifest{
		Subscribes: []manifest.EventSubscription{
			{Name: "sale.order.confirmed", Handler: "handle_confirmed_sync", Async: false},
		},
	})

	subs := r.Subscribers("sale.order.confirmed")
	if len(subs) != 2 {
		t.Fatalf("got %d subscribers, want 2", len(subs))
	}

	byModule := make(map[string]EventSubscription, len(subs))
	for _, s := range subs {
		byModule[s.ModuleName] = s
	}

	if !byModule["inventory"].Async {
		t.Error("inventory subscription Async = false, want true")
	}
	if byModule["audit"].Async {
		t.Error("audit subscription Async = true, want false")
	}
	if byModule["inventory"].HandlerName != "handle_confirmed_async" {
		t.Errorf("inventory HandlerName = %q, want %q", byModule["inventory"].HandlerName, "handle_confirmed_async")
	}
	if byModule["inventory"].Queue != "default" {
		t.Errorf("inventory Queue = %q, want %q", byModule["inventory"].Queue, "default")
	}
}

func TestEventRegistry_Register_RetryPolicyParsed(t *testing.T) {
	r := NewEventRegistry()

	r.Register("inventory", manifest.Manifest{
		Subscribes: []manifest.EventSubscription{
			{
				Name:                "sale.order.confirmed",
				Handler:             "handle_confirmed",
				Async:               true,
				IdempotencyKeyField: "event_id",
				RetryPolicy: &manifest.RetryPolicy{
					MaxAttempts:    5,
					Backoff:        "exponential",
					InitialDelayMS: 1000,
					MaxDelayMS:     300000,
					Jitter:         true,
				},
			},
		},
	})

	subs := r.Subscribers("sale.order.confirmed")
	if len(subs) != 1 {
		t.Fatalf("got %d subscribers, want 1", len(subs))
	}
	got := subs[0]

	if got.IdempotencyKeyField != "event_id" {
		t.Errorf("IdempotencyKeyField = %q, want %q", got.IdempotencyKeyField, "event_id")
	}
	want := RetryPolicy{
		MaxAttempts:  5,
		Backoff:      "exponential",
		InitialDelay: time.Second,
		MaxDelay:     300 * time.Second,
		Jitter:       true,
	}
	if got.RetryPolicy != want {
		t.Errorf("RetryPolicy = %+v, want %+v", got.RetryPolicy, want)
	}
}

func TestEventRegistry_Register_NilRetryPolicyDefaultsToZeroValue(t *testing.T) {
	r := NewEventRegistry()

	r.Register("inventory", manifest.Manifest{
		Subscribes: []manifest.EventSubscription{
			{Name: "sale.order.confirmed", Handler: "handle_confirmed"},
		},
	})

	subs := r.Subscribers("sale.order.confirmed")
	if len(subs) != 1 {
		t.Fatalf("got %d subscribers, want 1", len(subs))
	}
	if subs[0].RetryPolicy != (RetryPolicy{}) {
		t.Errorf("RetryPolicy = %+v, want zero value", subs[0].RetryPolicy)
	}
}
