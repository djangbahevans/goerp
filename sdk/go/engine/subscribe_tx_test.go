package engine

import (
	"errors"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/events"
)

func noopTxHandler(*db.Tx, events.Event[orderPayload]) error { return nil }

func TestSubscribeTx_RecordsTransactionalOptionsAndRoutingName(t *testing.T) {
	withFreshSubscriptions(t)

	def := events.Define[orderPayload]("sale.order.tx_recorded", events.Version(2))
	policy := events.RetryPolicy{
		MaxAttempts:  5,
		Backoff:      events.Exponential,
		InitialDelay: time.Second,
		MaxDelay:     time.Minute,
		NoJitter:     true,
	}
	SubscribeTx(def, noopTxHandler, Retry(policy), JobIdempotencyKey("event_id"))

	got := lastSubscription(t, def.Name(), def.Version())
	if !got.Async || !got.Transactional || got.Handler != "noopTxHandler" || got.IdempotencyKeyField != "event_id" {
		t.Fatalf("transactional registration = %+v", got)
	}
	if got.RetryPolicy == nil || got.RetryPolicy.MaxAttempts != 5 || got.RetryPolicy.Backoff != "exponential" ||
		got.RetryPolicy.InitialDelayMS != 1000 || got.RetryPolicy.MaxDelayMS != 60000 || !got.RetryPolicy.NoJitter {
		t.Fatalf("retry policy = %+v", got.RetryPolicy)
	}
}

func TestSubscribeTx_AnonymousHandlerAndVersionRouting(t *testing.T) {
	withFreshSubscriptions(t)

	name := "sale.order.tx_versions"
	called := false
	Subscribe(events.Define[orderPayload](name), noopHandler)
	SubscribeTx(events.Define[orderPayload](name, events.Version(2)), func(tx *db.Tx, evt events.Event[orderPayload]) error {
		called = tx.TxID() == "version-2-tx" && evt.Version == 2
		return nil
	})

	got := lastSubscription(t, name, 2)
	if got.Handler != name+".v2" || !got.Async || !got.Transactional || got.RetryPolicy != nil {
		t.Fatalf("declaration = %+v", got)
	}

	ptr, length := writeWireEvent(t, abi.EventEnvelope{
		Name:    name,
		Version: 2,
		TxID:    "version-2-tx",
		Payload: orderBytes(t, "order-2"),
	})
	defer Deallocate(ptr, length)

	if status := DispatchEvent(ptr, length); status != 0 || !called {
		t.Fatalf("status=%d called=%v, want the version 2 transactional handler", status, called)
	}
}

func TestSubscribeTx_InvalidOptionsLeaveRegistrationUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  SubscribeOption
	}{
		{name: "sync", opt: Sync()},
		{name: "invalid retry", opt: Retry(events.RetryPolicy{MaxAttempts: 0})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFreshSubscriptions(t)

			def := events.Define[orderPayload]("sale.order.tx_invalid")
			before := len(declaredSubscriptions(t, def.Name(), def.Version()))
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("invalid transactional options did not panic")
					}
				}()
				SubscribeTx(def, noopTxHandler, tc.opt)
			}()

			if len(subscriptions) != 0 || len(declaredSubscriptions(t, def.Name(), def.Version())) != before {
				t.Fatal("rejected registration published a handler or declaration")
			}

			SubscribeTx(def, noopTxHandler)
		})
	}
}

func TestSubscribeTx_DuplicatesAcrossRegistrationTypesPanic(t *testing.T) {
	for _, firstTx := range []bool{false, true} {
		for _, secondTx := range []bool{false, true} {
			withFreshSubscriptions(t)

			def := events.Define[orderPayload]("sale.order.tx_duplicate")
			register := func(transactional bool) {
				if transactional {
					SubscribeTx(def, noopTxHandler)
					return
				}

				Subscribe(def, noopHandler)
			}
			register(firstTx)
			before := len(declaredSubscriptions(t, def.Name(), def.Version()))
			original := subscriptions[subscriptionKey{def.Name(), def.Version()}]

			func() {
				defer func() {
					if recover() == nil {
						t.Fatalf("duplicate registration accepted: firstTx=%v secondTx=%v", firstTx, secondTx)
					}
				}()
				register(secondTx)
			}()

			if subscriptions[subscriptionKey{def.Name(), def.Version()}] != original ||
				len(declaredSubscriptions(t, def.Name(), def.Version())) != before {
				t.Fatal("duplicate registration replaced the handler or added a declaration")
			}
		}
	}
}

func TestDispatchEvent_TransactionalOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		noTx      bool
		badData   bool
		status    uint32
		wantCalls int
	}{
		{name: "success", status: 0, wantCalls: 1},
		{name: "retry", err: errors.New("database unavailable"), status: 1, wantCalls: 1},
		{name: "permanent", err: events.PermanentError(errors.New("invalid order")), status: 2, wantCalls: 1},
		{name: "retry after", err: events.RetryAfter(time.Minute, errors.New("rate limited")), status: 1, wantCalls: 1},
		{name: "missing transaction", noTx: true, status: 1},
		{name: "bad payload", badData: true, status: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFreshSubscriptions(t)

			want := events.Event[orderPayload]{
				ID:        "event-1",
				Name:      "sale.order.tx_outcome",
				Version:   2,
				EmitterID: "sales",
				TenantID:  "tenant-1",
				UserID:    "user-1",
				TraceID:   "trace-1",
				EmittedAt: time.Unix(1000, 0).UTC(),
				Payload:   orderPayload{OrderID: "order-1"},
			}
			calls := 0
			SubscribeTx(events.Define[orderPayload](want.Name, events.Version(2)), func(tx *db.Tx, evt events.Event[orderPayload]) error {
				calls++
				if evt.ID != want.ID || evt.Name != want.Name || evt.Version != want.Version ||
					evt.EmitterID != want.EmitterID || evt.TenantID != want.TenantID || evt.UserID != want.UserID ||
					evt.TraceID != want.TraceID || !evt.EmittedAt.Equal(want.EmittedAt) || evt.Payload != want.Payload ||
					tx.TxID() != "delivery-tx" {
					t.Fatalf("event=%+v transaction=%q", evt, tx.TxID())
				}

				return tc.err
			})

			wire := abi.EventEnvelope{
				ID:            want.ID,
				Name:          want.Name,
				Version:       want.Version,
				EmitterModule: want.EmitterID,
				TenantID:      want.TenantID,
				UserID:        want.UserID,
				TraceID:       want.TraceID,
				EmittedAt:     want.EmittedAt,
				Payload:       orderBytes(t, want.Payload.OrderID),
				TxID:          "delivery-tx",
			}
			if tc.noTx {
				wire.TxID = ""
			}
			if tc.badData {
				wire.Payload = []byte{0xc1}
			}
			ptr, length := writeWireEvent(t, wire)
			defer Deallocate(ptr, length)

			if status := DispatchEvent(ptr, length); status != tc.status || calls != tc.wantCalls {
				t.Fatalf("status=%d calls=%d, want status=%d calls=%d", status, calls, tc.status, tc.wantCalls)
			}
		})
	}
}
