package events

import "github.com/djangbahevans/goerp/sdk/go/events/def"

// Def is a typed event definition (see def.Def).
type Def[P any] = def.Def[P]

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

// Description sets a definition's manifest description.
func Description(text string) DefineOption { return def.Description(text) }

var (
	// WithDelay defers delivery by the given duration.
	WithDelay = def.WithDelay
	// WithIdempotencyKey supplies an explicit dedup key.
	WithIdempotencyKey = def.WithIdempotencyKey
)
