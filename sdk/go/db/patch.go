package db

import (
	"fmt"
	"reflect"
	"strings"
)

// Patch builds a partial UPDATE from a base record and a set of
// conditionally-supplied changes, tracking which fields actually changed.
type Patch struct {
	fields  []string
	values  []any
	whereID *string
}

// NewPatch starts a Patch. existingRecord is accepted for API symmetry
// with the doc's own worked examples (diffing against a base record);
// Patch itself only ever tracks what SetIfPresent is given.
func NewPatch(existingRecord any) *Patch {
	return &Patch{}
}

// SetIfPresent records field as changed to value, but only when value is
// non-nil (a nil pointer/interface means "not supplied" by the caller).
func (p *Patch) SetIfPresent(field string, value any) {
	if isNilValue(value) {
		return
	}
	p.fields = append(p.fields, field)
	p.values = append(p.values, value)
}

// HasChanges reports whether any field was actually set.
func (p *Patch) HasChanges() bool {
	return len(p.fields) > 0
}

// ChangedFields returns the fields set via SetIfPresent, in call order.
func (p *Patch) ChangedFields() []string {
	return p.fields
}

// Args returns ToUpdateSQL's own positional arguments, in order — the
// changed values, plus the id ToUpdateSQL was last called with. Panics if
// called with pending changes before ToUpdateSQL ever ran, since the
// returned slice would then be one short of the SQL it's meant to bind.
func (p *Patch) Args() []any {
	if p.whereID == nil {
		if len(p.fields) > 0 {
			panic("db: Patch.Args called before ToUpdateSQL")
		}
		return p.values
	}
	return append(append([]any{}, p.values...), *p.whereID)
}

// ToUpdateSQL returns a single parameterized "UPDATE table SET ...
// WHERE id = $n" statement for the fields set via SetIfPresent; call it
// before Args() so id is included as Args()'s own last value. Panics if
// nothing changed — check HasChanges() first.
func (p *Patch) ToUpdateSQL(table, id string) string {
	if len(p.fields) == 0 {
		panic("db: Patch.ToUpdateSQL called with no changes; check HasChanges() first")
	}
	sets := make([]string, len(p.fields))
	for i, field := range p.fields {
		sets[i] = fmt.Sprintf("%s = $%d", field, i+1)
	}
	p.whereID = &id
	return fmt.Sprintf("UPDATE %s SET %s WHERE id = $%d", table, strings.Join(sets, ", "), len(p.fields)+1)
}

// isNilValue reports whether v is a nil interface, or a typed nil of a
// kind that can be nil (pointer, interface, map, slice, chan, func).
func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}
