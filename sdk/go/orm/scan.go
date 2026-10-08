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

func (e *DecodeError) Error() string {
	return fmt.Sprintf("orm: %s.%s: cannot assign %T into %s", e.Struct, e.Field, e.Value, e.Expected)
}

// scanner is implemented by every generated model struct. Scan is exported
// because the generator emits it in the module's own package, which
// cannot implement an unexported interface method.
type scanner interface {
	Scan(row map[string]any) error
}

// ptrScanner constrains PT to a *T that implements scanner.
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
