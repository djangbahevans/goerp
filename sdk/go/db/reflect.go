package db

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// Struct mapping: a `db:"..."` tag names the column, `db:"-"` skips the
// field, and an untagged field's Go name is snake_cased
// (CustomerName -> customer_name).

var snakeCaseBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])|([A-Z]+)([A-Z][a-z])`)

// identifierRe matches a single bare SQL identifier. Table names and
// UpdateByID patch keys are interpolated into SQL text, and a patch map is
// often built from request input, so each is validated against this to
// rule out injection.
var identifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func validateIdentifier(name string) error {
	if !identifierRe.MatchString(name) {
		return fmt.Errorf("db: %q is not a valid SQL identifier", name)
	}
	return nil
}

func toSnakeCase(s string) string {
	return strings.ToLower(snakeCaseBoundary.ReplaceAllString(s, "${1}${3}_${2}${4}"))
}

// structField pairs one mapped struct field with the column name it maps
// to.
type structField struct {
	column string
	index  int // reflect.Value.Field(index)
}

// mappedFields returns t's exported fields not tagged `db:"-"`, in
// declaration order. t must be a struct type, not a pointer.
func mappedFields(t reflect.Type) ([]structField, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("db: %s is not a struct", t)
	}
	fields := make([]structField, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, ok := f.Tag.Lookup("db")
		if ok && tag == "-" {
			continue
		}
		column := tag
		if !ok || column == "" {
			column = toSnakeCase(f.Name)
		}
		if err := validateIdentifier(column); err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		fields = append(fields, structField{column: column, index: i})
	}
	return fields, nil
}

// structColumnsAndValues returns record's mapped column names and the
// matching values, in the same order. record is a struct or pointer to
// struct.
func structColumnsAndValues(record any) (columns []string, values []any, err error) {
	if record == nil {
		return nil, nil, fmt.Errorf("db: record is nil")
	}
	v := reflect.ValueOf(record)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil, fmt.Errorf("db: record is a nil %s", v.Type())
		}
		v = v.Elem()
	}
	fields, err := mappedFields(v.Type())
	if err != nil {
		return nil, nil, err
	}
	columns = make([]string, len(fields))
	values = make([]any, len(fields))
	for i, f := range fields {
		columns[i] = f.column
		values[i] = v.Field(f.index).Interface()
	}
	return columns, values, nil
}

// returningColumnsFor returns T's mapped column names in field order.
func returningColumnsFor[T any]() ([]string, error) {
	fields, err := mappedFields(reflect.TypeFor[T]())
	if err != nil {
		return nil, err
	}
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = f.column
	}
	return cols, nil
}

// scanRow populates a new T from row, matching each cols[i] against T's
// mapped column names. cols and row must be index-aligned.
func scanRow[T any](cols []string, row []any) (T, error) {
	var out T
	v := reflect.ValueOf(&out).Elem()
	if v.Kind() != reflect.Struct {
		return out, fmt.Errorf("db: %s is not a struct", v.Type())
	}
	fields, err := mappedFields(v.Type())
	if err != nil {
		return out, err
	}
	byColumn := make(map[string]structField, len(fields))
	for _, f := range fields {
		byColumn[f.column] = f
	}
	for i, col := range cols {
		if i >= len(row) {
			break
		}
		f, ok := byColumn[col]
		if !ok {
			continue // a column with no matching field is simply not scanned
		}
		if err := setFieldValue(v.Field(f.index), row[i]); err != nil {
			return out, fmt.Errorf("db: column %q: %w", col, err)
		}
	}
	return out, nil
}

// scanRows populates one T per row via scanRow.
func scanRows[T any](cols []string, rows [][]any) ([]T, error) {
	out := make([]T, len(rows))
	for i, row := range rows {
		v, err := scanRow[T](cols, row)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// setFieldValue assigns raw, one msgpack-decoded column value, to field.
// Nullable columns map to pointer fields: NULL leaves a pointer nil and is
// an error for any other field, so NULL is never confused with a zero
// value.
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
			// Only numeric conversions are intended here.
		default:
			field.Set(rv.Convert(field.Type()))
			return nil
		}
	}
	return fmt.Errorf("cannot assign %s into %s", rv.Type(), field.Type())
}
