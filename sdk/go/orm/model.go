package orm

// Model is implemented by every goerp module generate struct (goerp#977)
// — the binding every typed function in this package derives a resource
// name from (via a zero value's ResourceName()) instead of taking a
// separate "model string" argument that could name the wrong model.
type Model interface {
	ResourceName() string
}

// AnyField is satisfied by a Field[TModel, *] of any value type — used
// wherever a call site needs "some field of this model" without caring
// about its value type (Select, OrderBy).
type AnyField[TModel Model] interface {
	Name() string
}
