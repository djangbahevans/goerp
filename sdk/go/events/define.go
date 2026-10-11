package events

import "github.com/djangbahevans/goerp/sdk/go/events/def"

// Def binds an event's name and payload version to its payload type P.
type Def[P any] = def.Def[P]

// Definition exposes an event's name, version and payload type without fixing
// its payload type parameter, for APIs that accept different event definitions.
type Definition = def.Definition

// DefineOption configures Define.
type DefineOption = def.DefineOption

// EmitOption configures an emit.
type EmitOption = def.EmitOption

// Define declares an event named name whose payload is a P.
func Define[P any](name string, opts ...DefineOption) Def[P] {
	return def.Define[P](name, opts...)
}

// Version sets a definition's payload schema version (default 1).
func Version(v int) DefineOption { return def.Version(v) }

// IdempotencyKeyField names the payload field whose value is the emission's
// natural idempotency key.
func IdempotencyKeyField(field string) DefineOption { return def.IdempotencyKeyField(field) }

// Description sets a definition's manifest description.
func Description(text string) DefineOption { return def.Description(text) }

var (
	// WithDelay defers delivery by the given duration.
	WithDelay = def.WithDelay
	// WithIdempotencyKey supplies an explicit dedup key.
	WithIdempotencyKey = def.WithIdempotencyKey
)
