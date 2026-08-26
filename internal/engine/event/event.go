package event

import (
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

type EventRegistry struct {
	emitters             map[string][]string            // event_name → emitting module names
	subscribers          map[string][]EventSubscription // event_name → subscriptions
	moduleEmits          map[string]map[string]bool     // module_name → event_name → true
	emitIdempotencyField map[string]map[string]string   // module_name → event_name → idempotency_key_field
}

type EventSubscription struct {
	ModuleName          string
	HandlerName         string
	Async               bool
	Queue               string
	RetryPolicy         RetryPolicy
	IdempotencyKeyField string
}

// defaultMaxDelay is manifest-spec.md's documented max_delay_ms default (24h),
// applied when a subscription's retry_policy omits it.
const defaultMaxDelay = 24 * time.Hour

type RetryPolicy struct {
	MaxAttempts  int
	Backoff      string // "none" | "linear" | "exponential"
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Jitter       bool
}

func NewEventRegistry() *EventRegistry {
	return &EventRegistry{
		emitters:             make(map[string][]string),
		subscribers:          make(map[string][]EventSubscription),
		moduleEmits:          make(map[string]map[string]bool),
		emitIdempotencyField: make(map[string]map[string]string),
	}
}

func (r *EventRegistry) Register(moduleName string, m manifest.Manifest) {
	for _, emit := range m.Emits {
		r.emitters[emit.Name] = append(r.emitters[emit.Name], moduleName)

		if r.moduleEmits[moduleName] == nil {
			r.moduleEmits[moduleName] = make(map[string]bool)
		}
		r.moduleEmits[moduleName][emit.Name] = true

		if emit.IdempotencyKeyField != "" {
			if r.emitIdempotencyField[moduleName] == nil {
				r.emitIdempotencyField[moduleName] = make(map[string]string)
			}
			r.emitIdempotencyField[moduleName][emit.Name] = emit.IdempotencyKeyField
		}
	}

	for _, sub := range m.Subscribes {
		var retryPolicy RetryPolicy
		if sub.RetryPolicy != nil {
			maxDelay := time.Duration(sub.RetryPolicy.MaxDelayMS) * time.Millisecond
			if sub.RetryPolicy.MaxDelayMS == 0 {
				maxDelay = defaultMaxDelay
			}

			jitter := true
			if sub.RetryPolicy.Jitter != nil {
				jitter = *sub.RetryPolicy.Jitter
			}

			retryPolicy = RetryPolicy{
				MaxAttempts:  sub.RetryPolicy.MaxAttempts,
				Backoff:      sub.RetryPolicy.Backoff,
				InitialDelay: time.Duration(sub.RetryPolicy.InitialDelayMS) * time.Millisecond,
				MaxDelay:     maxDelay,
				Jitter:       jitter,
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

// IdempotencyKeyField returns the manifest-declared
// emits[].idempotency_key_field for (moduleName, eventName), if any.
func (r *EventRegistry) IdempotencyKeyField(moduleName, eventName string) (string, bool) {
	if fields, ok := r.emitIdempotencyField[moduleName]; ok {
		field, ok := fields[eventName]
		return field, ok
	}
	return "", false
}
