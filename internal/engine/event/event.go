package event

import (
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

type EventRegistry struct {
	emitters    map[string][]string            // event_name → emitting module names
	subscribers map[string][]EventSubscription // event_name → subscriptions
	moduleEmits map[string]map[string]bool     // module_name → event_name → true
}

type EventSubscription struct {
	ModuleName          string
	HandlerName         string
	Async               bool
	Queue               string
	RetryPolicy         RetryPolicy
	IdempotencyKeyField string
}

type RetryPolicy struct {
	MaxAttempts  int
	Backoff      string // "exponential" | "fixed"
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Jitter       bool
}

func NewEventRegistry() *EventRegistry {
	return &EventRegistry{
		emitters:    make(map[string][]string),
		subscribers: make(map[string][]EventSubscription),
		moduleEmits: make(map[string]map[string]bool),
	}
}

func (r *EventRegistry) Register(moduleName string, m manifest.Manifest) {
	for _, emit := range m.Emits {
		r.emitters[emit.Name] = append(r.emitters[emit.Name], moduleName)

		if r.moduleEmits[moduleName] == nil {
			r.moduleEmits[moduleName] = make(map[string]bool)
		}
		r.moduleEmits[moduleName][emit.Name] = true
	}

	for _, sub := range m.Subscribes {
		var retryPolicy RetryPolicy
		if sub.RetryPolicy != nil {
			retryPolicy = RetryPolicy{
				MaxAttempts:  sub.RetryPolicy.MaxAttempts,
				Backoff:      sub.RetryPolicy.Backoff,
				InitialDelay: time.Duration(sub.RetryPolicy.InitialDelayMS) * time.Millisecond,
				MaxDelay:     time.Duration(sub.RetryPolicy.MaxDelayMS) * time.Millisecond,
				Jitter:       sub.RetryPolicy.Jitter,
			}
		}

		r.subscribers[sub.Name] = append(r.subscribers[sub.Name], EventSubscription{
			ModuleName:          moduleName,
			HandlerName:         sub.Handler,
			Async:               sub.Async,
			Queue:               "default", // manifest has no per-subscription queue field yet
			RetryPolicy:         retryPolicy,
			IdempotencyKeyField: sub.IdempotencyKeyField,
		})
	}
}

func (r *EventRegistry) Subscribers(eventName string) []EventSubscription {
	if subs, ok := r.subscribers[eventName]; ok {
		return subs
	}

	return []EventSubscription{}
}

func (r *EventRegistry) Emitters(eventName string) []string {
	if emitters, ok := r.emitters[eventName]; ok {
		return emitters
	}

	return []string{}
}

func (r *EventRegistry) ModuleEmits(moduleName, eventName string) bool {
	if events, ok := r.moduleEmits[moduleName]; ok {
		if emits, ok := events[eventName]; ok {
			return emits
		}
	}

	return false
}
