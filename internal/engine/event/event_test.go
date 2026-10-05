package event

import (
	"slices"
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

	subs := r.Subscribers("sale.order.confirmed", 1)
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

	jitter := true
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
					Jitter:         &jitter,
				},
			},
		},
	})

	subs := r.Subscribers("sale.order.confirmed", 1)
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

	subs := r.Subscribers("sale.order.confirmed", 1)
	if len(subs) != 1 {
		t.Fatalf("got %d subscribers, want 1", len(subs))
	}
	if subs[0].RetryPolicy != (RetryPolicy{}) {
		t.Errorf("RetryPolicy = %+v, want zero value", subs[0].RetryPolicy)
	}
}

func TestEventRegistry_Register_RetryPolicyOmittedFieldsDefault(t *testing.T) {
	r := NewEventRegistry()

	r.Register("inventory", manifest.Manifest{
		Subscribes: []manifest.EventSubscription{
			{
				Name:    "sale.order.confirmed",
				Handler: "handle_confirmed",
				Async:   true,
				RetryPolicy: &manifest.RetryPolicy{
					MaxAttempts:    3,
					Backoff:        "linear",
					InitialDelayMS: 500,
					// MaxDelayMS and Jitter both omitted.
				},
			},
		},
	})

	subs := r.Subscribers("sale.order.confirmed", 1)
	if len(subs) != 1 {
		t.Fatalf("got %d subscribers, want 1", len(subs))
	}
	got := subs[0].RetryPolicy

	if got.MaxDelay != defaultMaxDelay {
		t.Errorf("MaxDelay = %v, want manifest-spec.md's documented default %v", got.MaxDelay, defaultMaxDelay)
	}
	if !got.Jitter {
		t.Errorf("Jitter = false, want manifest-spec.md's documented default true when omitted")
	}
}

func TestEventRegistry_Subscribers_MatchExactVersion(t *testing.T) {
	r := NewEventRegistry()
	r.Register("only_v2", manifest.Manifest{
		Subscribes: []manifest.EventSubscription{{Name: "sale.order.confirmed", Version: 2, Handler: "h2", Async: true}},
	})
	r.Register("only_v1", manifest.Manifest{
		Subscribes: []manifest.EventSubscription{{Name: "sale.order.confirmed", Handler: "h1", Async: true}},
	})
	r.Register("both", manifest.Manifest{
		Subscribes: []manifest.EventSubscription{
			{Name: "sale.order.confirmed", Version: 1, Handler: "both_v1", Async: true},
			{Name: "sale.order.confirmed", Version: 2, Handler: "both_v2", Async: true},
		},
	})

	handlers := func(version int) []string {
		var out []string
		for _, s := range r.Subscribers("sale.order.confirmed", version) {
			out = append(out, s.ModuleName+"/"+s.HandlerName)
		}
		return out
	}

	if got, want := handlers(1), []string{"only_v1/h1", "both/both_v1"}; !slices.Equal(got, want) {
		t.Errorf("v1 subscribers = %v, want %v (an omitted version defaults to 1)", got, want)
	}
	if got, want := handlers(2), []string{"only_v2/h2", "both/both_v2"}; !slices.Equal(got, want) {
		t.Errorf("v2 subscribers = %v, want %v", got, want)
	}
	if got := handlers(3); len(got) != 0 {
		t.Errorf("v3 subscribers = %v, want none", got)
	}
}
