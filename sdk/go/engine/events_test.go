package engine

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/events"
)

type orderPayload struct {
	OrderID string `msgpack:"order_id"`
}

func withFreshSubscriptions(t *testing.T) {
	t.Helper()
	origSubs, origOrder := subscriptions, subscriptionOrder
	subscriptions, subscriptionOrder = map[subscriptionKey]*registeredSubscription{}, nil
	t.Cleanup(func() { subscriptions, subscriptionOrder = origSubs, origOrder })
}

func noopHandler(events.Event[orderPayload]) error { return nil }

func TestSubscribe_RecordsRegistration(t *testing.T) {
	withFreshSubscriptions(t)

	policy := events.RetryPolicy{MaxAttempts: 5, Backoff: events.Exponential}
	def := events.Define[orderPayload]("sale.order.confirmed", events.Version(2))
	Subscribe(def, noopHandler, Sync(), Retry(policy), JobIdempotencyKey("event_id"))

	got := Subscriptions()
	if len(got) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(got))
	}
	s := got[0]
	if s.Event != "sale.order.confirmed" || s.Version != 2 || !s.Sync || s.Transactional ||
		s.JobIdempotencyKey != "event_id" || s.Retry == nil || *s.Retry != policy || s.Handler != "noopHandler" {
		t.Errorf("got %+v", s)
	}
}

func TestSubscribe_DefaultsAreAsyncWithNoPolicy(t *testing.T) {
	withFreshSubscriptions(t)

	Subscribe(events.Define[orderPayload]("sale.order.confirmed"), noopHandler)

	s := Subscriptions()[0]
	if s.Version != 1 || s.Sync || s.Retry != nil || s.JobIdempotencyKey != "" {
		t.Errorf("got %+v", s)
	}
}

func TestSubscribe_DuplicateNameAndVersionPanics(t *testing.T) {
	withFreshSubscriptions(t)

	def := events.Define[orderPayload]("sale.order.confirmed")
	Subscribe(def, noopHandler)

	defer func() {
		if recover() == nil {
			t.Error("second registration of the same (name, version) did not panic")
		}
	}()
	Subscribe(def, noopHandler)
}

func TestSubscribe_DifferentVersionsBothRegister(t *testing.T) {
	withFreshSubscriptions(t)

	Subscribe(events.Define[orderPayload]("sale.order.confirmed", events.Version(1)), noopHandler)
	Subscribe(events.Define[orderPayload]("sale.order.confirmed", events.Version(2)), noopHandler)

	if got := Subscriptions(); len(got) != 2 || got[0].Version != 1 || got[1].Version != 2 {
		t.Errorf("got %+v", got)
	}
}
