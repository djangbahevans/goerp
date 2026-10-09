package engine

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/declare"
	"github.com/djangbahevans/goerp/sdk/go/events"
	eventdef "github.com/djangbahevans/goerp/sdk/go/events/def"
)

// SubscribeOption configures a Subscribe or SubscribeTx registration.
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

// Subscription holds the options applied to a subscription registration.
type Subscription struct {
	Sync              bool
	Transactional     bool
	Retry             *events.RetryPolicy
	JobIdempotencyKey string
}

type subscriptionKey struct {
	event   string
	version int
}

type registeredSubscription struct {
	Subscription
	invoke func(abi.EventEnvelope) error
}

var subscriptions = map[subscriptionKey]*registeredSubscription{}

// Subscribe registers handler for def's event at def's version, called in
// init(). A delivery is routed by (event name, version), its payload
// decoded into P; a payload that fails to decode is a permanent failure
// and handler is not called. Registering the same (name, version) twice
// panics.
func Subscribe[P any](def events.Def[P], handler func(events.Event[P]) error, opts ...SubscribeOption) {
	registerSubscription(subscriptionKey{def.Name(), def.Version()}, handler, false, func(wire abi.EventEnvelope) error {
		evt, err := decodeEvent[P](wire)
		if err != nil {
			return err
		}

		return handler(evt)
	}, opts...)
}

// SubscribeTx registers an asynchronous handler for def's event and version.
// The handler receives an engine-managed transaction valid for its invocation.
// A nil return commits its database writes with the delivery ledger; an error
// rolls them back. Commit and Rollback on tx return db.transaction_managed
// errors. Effects outside tx are not covered by the delivery ledger.
// Register during init. Sync, an invalid retry policy, or a duplicate event
// name and version panics. An undecodable payload is a permanent failure;
// a delivery missing its transaction ID is retryable and does not call handler.
func SubscribeTx[P any](def events.Def[P], handler func(*db.Tx, events.Event[P]) error, opts ...SubscribeOption) {
	registerSubscription(subscriptionKey{def.Name(), def.Version()}, handler, true, func(wire abi.EventEnvelope) error {
		evt, err := decodeEvent[P](wire)
		if err != nil {
			return err
		}

		tx, err := db.NewManagedTx(wire.TxID)
		if err != nil {
			return err
		}

		return handler(tx, evt)
	}, opts...)
}

func decodeEvent[P any](wire abi.EventEnvelope) (events.Event[P], error) {
	evt := events.Event[P]{
		ID:        wire.ID,
		Name:      wire.Name,
		Version:   wire.Version,
		EmitterID: wire.EmitterModule,
		TenantID:  wire.TenantID,
		UserID:    wire.UserID,
		TraceID:   wire.TraceID,
		EmittedAt: wire.EmittedAt,
	}
	if err := unmarshal(wire.Payload, &evt.Payload); err != nil {
		return evt, events.PermanentError(fmt.Errorf("decode %s v%d payload: %w", wire.Name, wire.Version, err))
	}

	return evt, nil
}

func registerSubscription(key subscriptionKey, handler any, transactional bool, invoke func(abi.EventEnvelope) error, opts ...SubscribeOption) {
	if _, dup := subscriptions[key]; dup {
		panic(fmt.Sprintf("engine.Subscribe: %s v%d is already subscribed in this module", key.event, key.version))
	}

	sub := &registeredSubscription{invoke: invoke}
	for _, opt := range opts {
		opt(&sub.Subscription)
	}
	sub.Transactional = transactional
	if sub.Transactional && sub.Sync {
		panic(fmt.Sprintf("engine.SubscribeTx: %s v%d cannot use engine.Sync", key.event, key.version))
	}

	if sub.Retry != nil {
		validateRetryPolicy(key, *sub.Retry)
	}

	subscriptions[key] = sub
	declare.Add(eventdef.KindSubscription, subscriptionDeclaration(key, sub.Subscription, routingName(handler, fmt.Sprintf("%s.v%d", key.event, key.version))))
}

func validateRetryPolicy(key subscriptionKey, p events.RetryPolicy) {
	const minInitialDelay = 100 * time.Millisecond
	switch {
	case p.MaxAttempts < 1 || p.MaxAttempts > 25:
		panic(fmt.Sprintf("engine.Subscribe: %s v%d: RetryPolicy MaxAttempts %d must be 1-25", key.event, key.version, p.MaxAttempts))
	case p.Backoff != events.NoBackoff && p.Backoff != events.Linear && p.Backoff != events.Exponential:
		panic(fmt.Sprintf("engine.Subscribe: %s v%d: RetryPolicy Backoff %q must be none, linear or exponential", key.event, key.version, p.Backoff))
	case p.InitialDelay < minInitialDelay:
		panic(fmt.Sprintf("engine.Subscribe: %s v%d: RetryPolicy InitialDelay %s must be at least %s", key.event, key.version, p.InitialDelay, minInitialDelay))
	case p.InitialDelay%time.Millisecond != 0 || p.MaxDelay%time.Millisecond != 0 || p.MaxDelay < 0:
		panic(fmt.Sprintf("engine.Subscribe: %s v%d: RetryPolicy delays must be whole milliseconds", key.event, key.version))
	}
}

func subscriptionDeclaration(key subscriptionKey, s Subscription, handler string) eventdef.SubscriptionDeclaration {
	d := eventdef.SubscriptionDeclaration{
		Event:               key.event,
		Version:             key.version,
		Async:               !s.Sync,
		Transactional:       s.Transactional,
		IdempotencyKeyField: s.JobIdempotencyKey,
		Handler:             handler,
	}
	if r := s.Retry; r != nil {
		d.RetryPolicy = &eventdef.RetryPolicyDeclaration{
			MaxAttempts:    r.MaxAttempts,
			Backoff:        string(r.Backoff),
			InitialDelayMS: int(r.InitialDelay / time.Millisecond),
			MaxDelayMS:     int(r.MaxDelay / time.Millisecond),
			NoJitter:       r.NoJitter,
		}
	}
	return d
}

func handlerName(handler any, fallback string) string {
	fn := runtime.FuncForPC(reflect.ValueOf(handler).Pointer())
	if fn == nil {
		return fallback
	}
	_, base, found := strings.CutLast(fn.Name(), "/")
	if !found {
		base = fn.Name()
	}
	_, name, _ := strings.Cut(base, ".")
	return name
}
