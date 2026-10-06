package def

import (
	"encoding/json/v2"
	"reflect"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/declare"
	"github.com/djangbahevans/goerp/sdk/go/internal/payloadfields"
)

// Declaration kinds recorded into sdk/go/declare for the emits and subscribes
// collectors of `goerp module generate`.
const (
	KindEvent        = "event"
	KindSubscription = "subscription"
)

// EventDeclaration is a Define call as the manifest generator reads it.
// PayloadSchema maps each msgpack key of a struct payload to a type string,
// with "?" marking an optional field.
type EventDeclaration struct {
	Name          string         `json:"name"`
	Version       int            `json:"version"`
	Description   string         `json:"description,omitzero"`
	PayloadSchema map[string]any `json:"payload_schema,omitzero"`
	// IdempotencyKeyField is a key of PayloadSchema when the payload is a struct.
	IdempotencyKeyField string `json:"idempotency_key_field,omitzero"`
}

// RetryPolicyDeclaration is a subscription's retry policy in the manifest's
// units.
type RetryPolicyDeclaration struct {
	MaxAttempts    int    `json:"max_attempts"`
	Backoff        string `json:"backoff"`
	InitialDelayMS int    `json:"initial_delay_ms"`
	MaxDelayMS     int    `json:"max_delay_ms,omitzero"`
	NoJitter       bool   `json:"no_jitter,omitzero"`
}

// SubscriptionDeclaration is an engine.Subscribe call as the manifest
// generator reads it: one per registered event version.
type SubscriptionDeclaration struct {
	Event               string                  `json:"event"`
	Version             int                     `json:"version"`
	Async               bool                    `json:"async"`
	Transactional       bool                    `json:"transactional,omitzero"`
	RetryPolicy         *RetryPolicyDeclaration `json:"retry_policy,omitzero"`
	IdempotencyKeyField string                  `json:"idempotency_key_field,omitzero"`
	Handler             string                  `json:"handler"`
}

// lazyEventDeclaration derives the payload schema only when the registry is
// exported, which a running module never does.
type lazyEventDeclaration struct {
	EventDeclaration
	payload reflect.Type
}

func (l lazyEventDeclaration) MarshalJSON() ([]byte, error) {
	d := l.EventDeclaration
	d.PayloadSchema = payloadSchema(l.payload)
	return json.Marshal(d)
}

func declareEvent[P any](name string, d definition) {
	declare.Add(KindEvent, lazyEventDeclaration{
		Name:                name,
		Version:             d.version,
		Description:         d.description,
		IdempotencyKeyField: d.idempotencyKeyField,
		payload:             reflect.TypeFor[P](),
	})
}

func payloadSchema(t reflect.Type) map[string]any {
	fields, isStruct := payloadfields.Of(t)
	if !isStruct {
		return nil
	}

	schema := make(map[string]any, len(fields))
	for _, f := range fields {
		name := typeName(payloadfields.Deref(f.Type))
		if f.Optional {
			name += "?"
		}
		schema[f.Key] = name
	}
	return schema
}

func typeName(t reflect.Type) string {
	switch {
	case t == reflect.TypeFor[time.Time]():
		return "timestamp"
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8:
		return "bytes"
	case payloadfields.SelfEncoding(t):
		return "any"
	}

	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "uint"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Slice, reflect.Array:
		return "[]" + typeName(payloadfields.Deref(t.Elem()))
	case reflect.Map:
		return "map"
	case reflect.Struct:
		return "object"
	default:
		return "any"
	}
}
