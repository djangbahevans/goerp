package engine

import (
	"encoding/json/v2"
	"reflect"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/declare"
	"github.com/djangbahevans/goerp/sdk/go/events"
	eventdef "github.com/djangbahevans/goerp/sdk/go/events/def"
)

type orderPayload struct {
	OrderID string `msgpack:"order_id"`
}

func withFreshSubscriptions(t *testing.T) {
	t.Helper()
	origSubs := subscriptions
	subscriptions = map[subscriptionKey]*registeredSubscription{}
	t.Cleanup(func() { subscriptions = origSubs })
}

func noopHandler(events.Event[orderPayload]) error { return nil }

// declaredSubscriptions returns the subscription declarations recorded for
// event and version in sdk/go/declare, which no test resets.
func declaredSubscriptions(t *testing.T, event string, version int) []eventdef.SubscriptionDeclaration {
	t.Helper()
	data, err := declare.Export()
	if err != nil {
		t.Fatalf("declare.Export: %v", err)
	}
	var byKind map[string][]eventdef.SubscriptionDeclaration
	if err := json.Unmarshal(data, &byKind); err != nil {
		t.Fatalf("decode declarations: %v", err)
	}
	var out []eventdef.SubscriptionDeclaration
	for _, d := range byKind[eventdef.KindSubscription] {
		if d.Event == event && d.Version == version {
			out = append(out, d)
		}
	}
	return out
}

func lastSubscription(t *testing.T, event string, version int) eventdef.SubscriptionDeclaration {
	t.Helper()
	decls := declaredSubscriptions(t, event, version)
	if len(decls) == 0 {
		t.Fatalf("no subscription declaration for %s v%d", event, version)
	}
	return decls[len(decls)-1]
}

func TestSubscribe_RecordsTheDeclaration(t *testing.T) {
	withFreshSubscriptions(t)

	policy := events.RetryPolicy{MaxAttempts: 5, Backoff: events.Exponential, InitialDelay: time.Second, MaxDelay: time.Minute, NoJitter: true}
	def := events.Define[orderPayload]("sale.order.recorded", events.Version(2))
	Subscribe(def, noopHandler, Sync(), Retry(policy), JobIdempotencyKey("event_id"))

	got := lastSubscription(t, "sale.order.recorded", 2)
	want := eventdef.SubscriptionDeclaration{
		Event: "sale.order.recorded", Version: 2, Async: false, IdempotencyKeyField: "event_id", Handler: "noopHandler",
		RetryPolicy: &eventdef.RetryPolicyDeclaration{MaxAttempts: 5, Backoff: "exponential", InitialDelayMS: 1000, MaxDelayMS: 60000, NoJitter: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("declaration = %+v (retry %+v), want %+v (retry %+v)", got, got.RetryPolicy, want, want.RetryPolicy)
	}
}

func TestSubscribe_DefaultsAreAsyncWithNoPolicy(t *testing.T) {
	withFreshSubscriptions(t)

	Subscribe(events.Define[orderPayload]("sale.order.defaults"), noopHandler)

	got := lastSubscription(t, "sale.order.defaults", 1)
	if !got.Async || got.RetryPolicy != nil || got.IdempotencyKeyField != "" || got.Transactional {
		t.Errorf("got %+v", got)
	}
}

func TestSubscribe_AnonymousHandlerIsNamedByEventAndVersion(t *testing.T) {
	withFreshSubscriptions(t)

	Subscribe(events.Define[orderPayload]("sale.order.anonymous", events.Version(3)), func(events.Event[orderPayload]) error { return nil })

	if got := lastSubscription(t, "sale.order.anonymous", 3).Handler; got != "sale.order.anonymous.v3" {
		t.Errorf("Handler = %q, want a name that does not depend on the closure's position", got)
	}
}

func TestSubscribe_DuplicateNameAndVersionPanics(t *testing.T) {
	withFreshSubscriptions(t)

	def := events.Define[orderPayload]("sale.order.duplicate")
	Subscribe(def, noopHandler)
	before := len(declaredSubscriptions(t, "sale.order.duplicate", 1))

	defer func() {
		if recover() == nil {
			t.Error("second registration of the same (name, version) did not panic")
		}
		if got := len(declaredSubscriptions(t, "sale.order.duplicate", 1)); got != before {
			t.Errorf("a rejected registration recorded a declaration: %d, want %d", got, before)
		}
	}()
	Subscribe(def, noopHandler)
}

func TestSubscribe_DifferentVersionsEachRecordADeclaration(t *testing.T) {
	withFreshSubscriptions(t)

	Subscribe(events.Define[orderPayload]("sale.order.versions", events.Version(1)), noopHandler)
	Subscribe(events.Define[orderPayload]("sale.order.versions", events.Version(2)), noopHandler)

	if len(declaredSubscriptions(t, "sale.order.versions", 1)) != 1 || len(declaredSubscriptions(t, "sale.order.versions", 2)) != 1 {
		t.Error("each registered version should record one subscription declaration")
	}
}

func TestSubscribe_InvalidRetryPolicyPanics(t *testing.T) {
	valid := events.RetryPolicy{MaxAttempts: 3, Backoff: events.Linear, InitialDelay: time.Second}
	tests := []struct {
		name   string
		mutate func(*events.RetryPolicy)
	}{
		{"zero attempts", func(p *events.RetryPolicy) { p.MaxAttempts = 0 }},
		{"too many attempts", func(p *events.RetryPolicy) { p.MaxAttempts = 26 }},
		{"unknown backoff", func(p *events.RetryPolicy) { p.Backoff = "fast" }},
		{"initial delay under 100ms", func(p *events.RetryPolicy) { p.InitialDelay = 50 * time.Millisecond }},
		{"fractional millisecond delay", func(p *events.RetryPolicy) { p.InitialDelay = time.Second + time.Microsecond }},
		{"negative max delay", func(p *events.RetryPolicy) { p.MaxDelay = -time.Second }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFreshSubscriptions(t)
			policy := valid
			tt.mutate(&policy)

			defer func() {
				if recover() == nil {
					t.Error("an invalid retry policy was accepted")
				}
			}()
			Subscribe(events.Define[orderPayload]("sale.order.retry"), noopHandler, Retry(policy))
		})
	}
}

func TestSubscribe_ValidRetryPolicyIsAccepted(t *testing.T) {
	withFreshSubscriptions(t)

	Subscribe(events.Define[orderPayload]("sale.order.retry_ok"), noopHandler,
		Retry(events.RetryPolicy{MaxAttempts: 25, Backoff: events.NoBackoff, InitialDelay: 100 * time.Millisecond}))
}
