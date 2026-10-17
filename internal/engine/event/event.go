package event

import (
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

type EventRegistry struct {
	emitters             map[string][]string
	subscribers          map[string][]EventSubscription
	moduleEmits          map[string]map[eventKey]bool
	emitIdempotencyField map[string]map[string]string
}

type eventKey struct {
	name    string
	version int
}

type EventSubscription struct {
	ModuleName string
	// Version is zero for system events.
	Version             int
	HandlerName         string
	Async               bool
	Transactional       bool
	Queue               string
	RetryPolicy         RetryPolicy
	IdempotencyKeyField string
}

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
		moduleEmits:          make(map[string]map[eventKey]bool),
		emitIdempotencyField: make(map[string]map[string]string),
	}
}

func (r *EventRegistry) Register(moduleName string, m manifest.Manifest) {
	for _, emit := range m.Emits {
		r.emitters[emit.Name] = append(r.emitters[emit.Name], moduleName)

		if r.moduleEmits[moduleName] == nil {
			r.moduleEmits[moduleName] = make(map[eventKey]bool)
		}
		r.moduleEmits[moduleName][eventKey{emit.Name, emit.EffectiveVersion()}] = true

		if emit.IdempotencyKeyField != "" {
			if r.emitIdempotencyField[moduleName] == nil {
				r.emitIdempotencyField[moduleName] = make(map[string]string)
			}
			r.emitIdempotencyField[moduleName][emit.Name] = emit.IdempotencyKeyField
		}
	}

	for _, sub := range m.Subscribes {
		r.subscribers[sub.Name] = append(r.subscribers[sub.Name], EventSubscription{
			ModuleName:          moduleName,
			Version:             sub.EffectiveVersion(),
			HandlerName:         sub.Handler,
			Async:               sub.Async,
			Transactional:       sub.Transactional,
			Queue:               "default",
			RetryPolicy:         retryPolicyFromManifest(sub.RetryPolicy),
			IdempotencyKeyField: sub.IdempotencyKeyField,
		})
	}
}

func (r *EventRegistry) Subscribers(eventName string, version int) []EventSubscription {
	subs := []EventSubscription{}
	for _, sub := range r.subscribers[eventName] {
		if strings.HasPrefix(eventName, "system.") || sub.Version == version {
			subs = append(subs, sub)
		}
	}
	return subs
}

func (r *EventRegistry) Emitters(eventName string) []string {
	if emitters, ok := r.emitters[eventName]; ok {
		return emitters
	}

	return []string{}
}

func (r *EventRegistry) ModuleEmits(moduleName, eventName string, version int) bool {
	return r.moduleEmits[moduleName][eventKey{eventName, version}]
}

func (r *EventRegistry) IdempotencyKeyField(moduleName, eventName string) (string, bool) {
	if fields, ok := r.emitIdempotencyField[moduleName]; ok {
		field, ok := fields[eventName]
		return field, ok
	}
	return "", false
}
