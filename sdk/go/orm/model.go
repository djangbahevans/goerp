package orm

import "github.com/djangbahevans/goerp/sdk/go/model"

// Model is implemented by every struct `goerp module generate` emits.
// Typed functions in this package derive the model name from a zero
// value's ResourceName(), so a call can never name the wrong model.
type Model = model.Named

// AnyField is satisfied by a Field[TModel, *] of any value type — used
// wherever a call site needs "some field of this model" without caring
// about its value type (Select, OrderBy).
type AnyField[TModel Model] interface {
	Name() string
}
