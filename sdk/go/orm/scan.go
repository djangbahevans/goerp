package orm

import "fmt"

// DecodeError reports a generated struct's Scan method failing to
// type-assert one field's raw decoded value into its declared Go type.
type DecodeError struct {
	Struct   string // generated struct name, e.g. "Gadget"
	Field    string // Go field name, e.g. "Price"
	Expected string // expected Go type, e.g. "float64"
	Value    any    // the raw value actually present in the decoded row
}

// NewDecodeError builds a DecodeError for field on structName, given the
// value actually found in place of the expected type.
func NewDecodeError(structName, field, expected string, value any) *DecodeError {
	return &DecodeError{Struct: structName, Field: field, Expected: expected, Value: value}
}

// Error renders the same "cannot assign %s into %s" shape reflect.go's
// former runtime-reflection decode path produced, restructured as typed
// fields instead of a single opaque string.
func (e *DecodeError) Error() string {
	return fmt.Sprintf("orm: %s.%s: cannot assign %T into %s", e.Struct, e.Field, e.Value, e.Expected)
}

// scanner is what every decode call site (Query.All/.One, Get/GetMany,
// Create, Mutate, FirstOrCreate — issues #975/#976) requires *T to
// implement, via the ptrScanner constraint below, instead of T being
// constrained to Model alone. Scan is exported — unlike this interface
// itself — because goerp module generate (issue #977) emits it onto a
// struct in the module's own models package, a different package than
// this one: an unexported interface method can only be satisfied by a
// type declared in the same package as the interface, so a lowercase
// scan here could never be implemented from outside sdk/go/orm.
type scanner interface {
	Scan(row map[string]any) error
}

// ptrScanner is decodeRecord/decodeRecords' PT type parameter: a pointer
// to T that implements scanner. Its own named constraint, rather than
// repeating "*T; scanner" as an inline interface literal at every decode
// call site (search.go, read.go, create.go, mutate.go, firstorcreate.go).
type ptrScanner[T any] interface {
	*T
	scanner
}

// decodeRecord populates a new T from rec via T's own Scan method.
func decodeRecord[T any, PT ptrScanner[T]](rec map[string]any) (T, error) {
	var out T
	if err := PT(&out).Scan(rec); err != nil {
		return out, err
	}
	return out, nil
}

// decodeRecords populates one T per record via T's own Scan method.
func decodeRecords[T any, PT ptrScanner[T]](recs []map[string]any) ([]T, error) {
	if len(recs) == 0 {
		return nil, nil
	}
	out := make([]T, len(recs))
	for i, rec := range recs {
		if err := PT(&out[i]).Scan(rec); err != nil {
			return nil, fmt.Errorf("orm: record %d: %w", i, err)
		}
	}
	return out, nil
}
