package orm

// RelationRef is a Many2One field's expanded read-time object:
// {id, display_name}.
type RelationRef struct {
	ID          string `db:"id"`
	DisplayName string `db:"display_name"`
}

// Ref is a Many2One expansion field, typed to its target model so a
// Ref[Gadget] and a Ref[Widget] are not interchangeable. For a same-module
// target T is the target's generated struct; for a cross-module target it
// is a generated marker type carrying only ResourceName(), because modules
// build independently and cannot import each other's structs. Expand
// returns only {id, display_name}, never T's other fields. The zero value
// has a nil RelationRef, meaning there was nothing to expand.
type Ref[T Model] struct{ *RelationRef }

// Expand exposes the underlying {id, display_name} pair Ref wraps.
func (r Ref[T]) Expand() *RelationRef { return r.RelationRef }

// FetchRef resolves ref's ID into its full T record via Get. PT's
// scanner constraint is only satisfiable by a real generated model
// struct — never by a cross-module marker type, which carries no column
// data for a Scan method to populate — so a Ref[T] built from a marker
// type fails to compile here rather than at some later call site.
func FetchRef[T Model, PT ptrScanner[T]](ref Ref[T], fields ...AnyField[T]) (T, error) {
	if ref.RelationRef == nil {
		var zero T
		return zero, ErrNotFound
	}
	return Get[T, PT](ref.ID, fields...)
}
