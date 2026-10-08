package orm

// Values holds the field values for Create, Write, WriteMany, WriteWhere
// and FirstOrCreate. Set and SetBytes check each value against its field's
// Go type at compile time. Start one with NewValues.
type Values[T Model] struct {
	m map[string]any
}

// NewValues starts an empty Values for T.
func NewValues[T Model]() *Values[T] {
	return &Values[T]{m: make(map[string]any)}
}

// raw returns v's field values in the map form host call inputs take.
func (v *Values[T]) raw() map[string]any {
	if v == nil {
		return nil
	}
	return v.m
}

// Set assigns value to the column f names on v and returns v.
func (v *Values[T]) Set[TValue any](f Field[T, TValue], value TValue) *Values[T] {
	if v.m == nil {
		v.m = make(map[string]any)
	}
	v.m[f.Name()] = value
	return v
}

// SetBytes is Set's counterpart for BytesField, which has no comparison
// methods and so can't satisfy Set's Field[T, TValue] parameter.
func (v *Values[T]) SetBytes(f BytesField[T], value []byte) *Values[T] {
	if v.m == nil {
		v.m = make(map[string]any)
	}
	v.m[f.Name()] = value
	return v
}

// resourceName returns the model name host call inputs take, derived from
// T so it cannot disagree with the typed arguments.
func resourceName[T Model]() string {
	var zero T
	return zero.ResourceName()
}
