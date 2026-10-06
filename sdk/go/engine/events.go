package engine

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/events"
)

// SubscribeOption configures a Subscribe registration — Sync, Retry,
// JobIdempotencyKey.
type SubscribeOption func(*Subscription)

// Sync marks the subscription as eligible for inline dispatch. Unset, a
// subscription is asynchronous.
func Sync() SubscribeOption {
	return func(s *Subscription) { s.Sync = true }
}

// Retry sets the subscription's retry policy (manifest retry_policy).
func Retry(policy events.RetryPolicy) SubscribeOption {
	return func(s *Subscription) { s.Retry = &policy }
}

// JobIdempotencyKey sets the envelope field used as the delivery job's
// idempotency key (manifest idempotency_key_field), e.g. "event_id".
func JobIdempotencyKey(field string) SubscribeOption {
	return func(s *Subscription) { s.JobIdempotencyKey = field }
}

// Subscription records one Subscribe registration for manifest generation.
type Subscription struct {
	Event             string
	Version           int
	Sync              bool
	Transactional     bool
	Retry             *events.RetryPolicy
	JobIdempotencyKey string
	// Handler is the SDK routing name written to the manifest's handler.
	Handler string
}

type subscriptionKey struct {
	event   string
	version int
}

type registeredSubscription struct {
	Subscription
	invoke func(abi.EventEnvelope) error
}

var (
	subscriptions     = map[subscriptionKey]*registeredSubscription{}
	subscriptionOrder []subscriptionKey
)

// Subscribe registers handler for def's event at def's version, called in
// init(). A delivery is routed by (event name, version), its payload
// decoded into P; a payload that fails to decode is a permanent failure
// and handler is not called. Registering the same (name, version) twice
// panics.
func Subscribe[P any](def events.Def[P], handler func(events.Event[P]) error, opts ...SubscribeOption) {
	key := subscriptionKey{def.Name(), def.Version()}
	if _, dup := subscriptions[key]; dup {
		panic(fmt.Sprintf("engine.Subscribe: %s v%d is already subscribed in this module", key.event, key.version))
	}

	sub := &registeredSubscription{
		Event: key.event, Version: key.version, Handler: handlerName(handler, "handle_event"),
		invoke: func(wire abi.EventEnvelope) error {
			evt := events.Event[P]{
				ID: wire.ID, Name: wire.Name, Version: wire.Version, EmitterID: wire.EmitterModule,
				TenantID: wire.TenantID, UserID: wire.UserID, TraceID: wire.TraceID, EmittedAt: wire.EmittedAt,
			}
			if err := unmarshal(wire.Payload, &evt.Payload); err != nil {
				return events.PermanentError(fmt.Errorf("decode %s v%d payload: %w", wire.Name, wire.Version, err))
			}
			return handler(evt)
		},
	}
	for _, opt := range opts {
		opt(&sub.Subscription)
	}

	subscriptions[key] = sub
	subscriptionOrder = append(subscriptionOrder, key)
}

// Subscriptions returns every registration in registration order.
func Subscriptions() []Subscription {
	out := make([]Subscription, 0, len(subscriptionOrder))
	for _, key := range subscriptionOrder {
		sub := subscriptions[key].Subscription
		if sub.Retry != nil {
			sub.Retry = new(*sub.Retry)
		}
		out = append(out, sub)
	}
	return out
}

// handlerName returns handler's function name without its package path,
// or fallback when the runtime has none.
func handlerName(handler any, fallback string) string {
	fn := runtime.FuncForPC(reflect.ValueOf(handler).Pointer())
	if fn == nil {
		return fallback
	}
	_, name, _ := strings.Cut(fn.Name()[strings.LastIndex(fn.Name(), "/")+1:], ".")
	return name
}
