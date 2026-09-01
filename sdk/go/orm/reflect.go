package orm

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// Struct-tag-driven record mapping — sdk/go/db/reflect.go's own
// mappedFields/scanRow[T]/setFieldValue, duplicated rather than imported
// (this file's own package doc comment precedent): a record here is a
// map[string]any keyed by column name, not a positional row, so
// map-lookup replaces column-index lookup, but the db:"..." tag, "-" to
// skip, and snake_case fallback for untagged fields all match
// go-sdk-reference.md §6's own "Struct mapping" rules exactly.

var snakeCaseBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])|([A-Z]+)([A-Z][a-z])`)

func toSnakeCase(s string) string {
	return strings.ToLower(snakeCaseBoundary.ReplaceAllString(s, "${1}${3}_${2}${4}"))
}

// ormField pairs one mapped struct field with the record key it maps to.
type ormField struct {
	key   string
	index int
}

// ormFields returns t's own db-tag-mapped fields, in declaration order —
// every exported field, skipping any tagged `db:"-"`. t must be a struct
// type (not a pointer).
func ormFields(t reflect.Type) ([]ormField, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("orm: %s is not a struct", t)
	}
	fields := make([]ormField, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, ok := f.Tag.Lookup("db")
		if ok && tag == "-" {
			continue
		}
		key := tag
		if !ok || key == "" {
			key = toSnakeCase(f.Name)
		}
		fields = append(fields, ormField{key: key, index: i})
	}
	return fields, nil
}

// decodeRecord populates a new T from rec, matching each of T's own
// db-tag-mapped fields against rec's own keys — a record key with no
// matching field is simply not scanned, the same as db/reflect.go's own
// scanRow.
func decodeRecord[T any](rec map[string]any) (T, error) {
	var out T
	v := reflect.ValueOf(&out).Elem()
	if v.Kind() != reflect.Struct {
		return out, fmt.Errorf("orm: %s is not a struct", v.Type())
	}
	fields, err := ormFields(v.Type())
	if err != nil {
		return out, err
	}
	if err := populateRecord(v, fields, rec); err != nil {
		return out, err
	}
	return out, nil
}

// decodeRecords populates one T per record — T's own db-tag-mapped
// fields are computed once via reflection, not once per record.
func decodeRecords[T any](recs []map[string]any) ([]T, error) {
	if len(recs) == 0 {
		return nil, nil
	}
	fields, err := ormFields(reflect.TypeFor[T]())
	if err != nil {
		return nil, err
	}
	out := make([]T, len(recs))
	for i, rec := range recs {
		v := reflect.ValueOf(&out[i]).Elem()
		if err := populateRecord(v, fields, rec); err != nil {
			return nil, fmt.Errorf("orm: record %d: %w", i, err)
		}
	}
	return out, nil
}

// populateRecord assigns rec's own values into v's fields (v addresses a
// struct of the type fields was computed from) — decodeRecord/
// decodeRecords' shared per-record step.
func populateRecord(v reflect.Value, fields []ormField, rec map[string]any) error {
	for _, f := range fields {
		raw, ok := rec[f.key]
		if !ok {
			continue
		}
		if err := setFieldValue(v.Field(f.index), raw); err != nil {
			return fmt.Errorf("orm: field %q: %w", f.key, err)
		}
	}
	return nil
}

// setFieldValue assigns raw (one value from a msgpack-decoded record)
// into field. field may be a pointer type for a nullable column
// (go-sdk-reference.md §6's "pointer for nullable" convention) — raw ==
// nil leaves a pointer field nil; a non-pointer field can't represent
// NULL, so it errors rather than silently zero-valuing.
func setFieldValue(field reflect.Value, raw any) error {
	if raw == nil {
		if field.Kind() != reflect.Pointer {
			return fmt.Errorf("cannot assign NULL into non-pointer field type %s", field.Type())
		}
		return nil
	}
	if field.Kind() == reflect.Pointer {
		elem := reflect.New(field.Type().Elem())
		if err := setFieldValue(elem.Elem(), raw); err != nil {
			return err
		}
		field.Set(elem)
		return nil
	}

	rv := reflect.ValueOf(raw)
	if rv.Type().AssignableTo(field.Type()) {
		field.Set(rv)
		return nil
	}
	if rv.Type().ConvertibleTo(field.Type()) {
		switch field.Kind() {
		case reflect.String, reflect.Bool, reflect.Struct:
			// A ConvertibleTo pass for these kinds is almost always a
			// coincidental method-set match, not a real, intended
			// conversion — restrict conversion to the numeric kinds it's
			// actually meant for.
		default:
			field.Set(rv.Convert(field.Type()))
			return nil
		}
	}
	return fmt.Errorf("cannot assign %s into %s", rv.Type(), field.Type())
}
