package orm

// Values is a Model-typed replacement for the "vals map[string]any"
// argument Create/Write/WriteMany/WriteWhere/FirstOrCreate took before —
// built up field by field via Set/SetBytes, each of which checks the
// field's own declared Go type at compile time instead of a raw
// map[string]any key/value pair the engine only validates at runtime.
// Package-private construction — a caller always starts one via
// NewValues, never builds the struct literal directly.
type Values[T Model] struct {
	m map[string]any
}

// NewValues starts an empty Values for T.
func NewValues[T Model]() *Values[T] {
	return &Values[T]{m: make(map[string]any)}
}

// raw returns v's accumulated field values, for the hostcall input
// types (which still take map[string]any — that boundary is unchanged
// by this typed wrapper).
func (v *Values[T]) raw() map[string]any {
	if v == nil {
		return nil
	}
	return v.m
}

// Set assigns value to the column f names on v. A free function, not a
// method — Go doesn't allow a method to introduce a type parameter
// beyond its receiver's, and TValue varies per call while T is fixed on
// Values[T].
func Set[T Model, TValue any](v *Values[T], f Field[T, TValue], value TValue) *Values[T] {
	if v.m == nil {
		v.m = make(map[string]any)
	}
	v.m[f.Name()] = value
	return v
}

// SetBytes is Set's counterpart for BytesField, which has no comparison
// methods and so can't satisfy Set's Field[T, TValue] parameter.
func SetBytes[T Model](v *Values[T], f BytesField[T], value []byte) *Values[T] {
	if v.m == nil {
		v.m = make(map[string]any)
	}
	v.m[f.Name()] = value
	return v
}

// resourceName returns T's own ResourceName() — the model string every
// hostcall input still takes, derived from a zero value instead of a
// separate argument a caller could mismatch against T.
func resourceName[T Model]() string {
	var zero T
	return zero.ResourceName()
}
