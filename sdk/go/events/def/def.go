// Package def holds the host-call-free half of the events SDK: typed event
// definitions and the emit options. A module's schema package imports it
// to name an event without linking host functions (go-sdk-reference.md §7
// "Defining an event", §22 "Package layout"). The Def emit methods
// delegate to an Emitter that sdk/go/events installs when it is linked.
package def

import (
	"errors"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

// DefaultVersion is the payload version of a definition that does not set
// one with Version.
const DefaultVersion = 1

// ErrNoEmitter is returned by a Def emit method when no Emitter is
// installed, i.e. sdk/go/events is not linked into the binary.
var ErrNoEmitter = errors.New("sdk/go/events/def: no emitter installed; import sdk/go/events to emit events")

// Tx identifies an open database transaction. *db.Tx satisfies it.
type Tx interface {
	TxID() string
}

// Emitter performs the host calls behind Def's emit methods.
type Emitter interface {
	Emit(in abi.EventEmitInput) (string, error)
	EmitTx(txID string, in abi.EventEmitInput) (string, error)
}

var emitter Emitter

// SetEmitter installs the Emitter the Def emit methods delegate to. It is
// called from sdk/go/events's init.
func SetEmitter(e Emitter) { emitter = e }

// Def is a typed event definition binding an event's name, payload version
// and payload type P.
type Def[P any] struct {
	name        string
	version     int
	description string
}

// DefineOption configures Define — Version, Description.
type DefineOption func(*definition)

type definition struct {
	version     int
	description string
}

// Version sets the definition's payload schema version. Unset defaults to
// DefaultVersion.
func Version(v int) DefineOption {
	return func(d *definition) { d.version = v }
}

// Description sets the human-readable description carried into the
// manifest's emits entry.
func Description(text string) DefineOption {
	return func(d *definition) { d.description = text }
}

// Define declares an event named name whose payload is a P.
func Define[P any](name string, opts ...DefineOption) Def[P] {
	d := definition{version: DefaultVersion}
	for _, opt := range opts {
		opt(&d)
	}
	return Def[P]{name: name, version: d.version, description: d.description}
}

// Name returns the event's name.
func (d Def[P]) Name() string { return d.name }

// Version returns the event's payload schema version.
func (d Def[P]) Version() int { return d.version }

// Description returns the event's manifest description.
func (d Def[P]) Description() string { return d.description }

// EmitOption configures an emit — WithDelay, WithIdempotencyKey.
type EmitOption func(*abi.EventEmitInput)

// WithDelay defers delivery by delay.
func WithDelay(delay time.Duration) EmitOption {
	return func(in *abi.EventEmitInput) { in.DelayMs = int(delay / time.Millisecond) }
}

// WithIdempotencyKey supplies an explicit dedup key, taking precedence
// over any manifest-declared idempotency_key_field for this event
// (event-system.md §4).
func WithIdempotencyKey(key string) EmitOption {
	return func(in *abi.EventEmitInput) { in.IdempotencyKey = key }
}

// EmitTx emits the event scoped to tx — visible to other work in the same
// transaction, delivered only once tx commits. Returns the event ID.
func (d Def[P]) EmitTx(tx Tx, payload P, opts ...EmitOption) (string, error) {
	if emitter == nil {
		return "", ErrNoEmitter
	}
	in, err := d.input(payload, opts)
	if err != nil {
		return "", err
	}
	return emitter.EmitTx(tx.TxID(), in)
}

// Emit emits the event outside any transaction. Returns the event ID.
func (d Def[P]) Emit(payload P, opts ...EmitOption) (string, error) {
	if emitter == nil {
		return "", ErrNoEmitter
	}
	in, err := d.input(payload, opts)
	if err != nil {
		return "", err
	}
	return emitter.Emit(in)
}

// EmitSync emits the event outside any transaction and runs its
// synchronous (async:false) subscribers inline, returning their aggregated
// failure as the error. There is no transactional form: inline dispatch
// would fire subscribers for a write that later rolls back.
func (d Def[P]) EmitSync(payload P, opts ...EmitOption) (string, error) {
	if emitter == nil {
		return "", ErrNoEmitter
	}
	in, err := d.input(payload, opts)
	if err != nil {
		return "", err
	}
	in.Sync = true
	return emitter.Emit(in)
}

func (d Def[P]) input(payload P, opts []EmitOption) (abi.EventEmitInput, error) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return abi.EventEmitInput{}, err
	}
	in := abi.EventEmitInput{Name: d.name, Version: d.version, Payload: data}
	for _, opt := range opts {
		opt(&in)
	}
	return in, nil
}
