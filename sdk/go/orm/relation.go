package orm

// RelationRef is a Many2One field's expanded read-time object —
// go-sdk-reference.md §22 "Many2One": {id, display_name}.
type RelationRef struct {
	ID          string `db:"id"`
	DisplayName string `db:"display_name"`
}

// Ref is a Many2One expansion field, typed to the target model it points
// at so a Ref[Gadget] and a Ref[Widget] cannot be assigned to each
// other's struct field. T is Gadget's own generated struct for a
// same-module target, or a generated local zero-field marker type
// carrying nothing but ResourceName() for a cross-module target
// (goerp module generate, goerp#979) — modules build independently, with
// no shared source tree, so a cross-module target's real struct is never
// importable. Expand always returns just {id, display_name} — the
// entirety of what host.orm ever returns for a Many2One expansion
// (go-sdk-reference.md §22), same-module or cross-module; it never
// carries T's other fields. The zero value has a nil RelationRef,
// decoded that way whenever there's nothing to expand.
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
