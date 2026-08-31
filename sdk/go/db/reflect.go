package db

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// Struct-tag-driven column mapping shared by ExecReturning/InsertReturning
// (populating a struct from a returned row) and Insert/InsertReturning
// (building a column list and parameter values from a struct) —
// go-sdk-reference.md §6's own "Struct mapping" rules: a `db:"..."` tag
// names the column explicitly, `db:"-"` skips the field, and an untagged
// field's own Go name is snake_cased (CustomerName -> customer_name).

var snakeCaseBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])|([A-Z]+)([A-Z][a-z])`)

// identifierRe matches a single bare SQL identifier — the same shape
// host.db.exec's own returningColumnRe (internal/engine/wasm/
// host_db_exec.go) accepts for opts.returning. Table names and
// UpdateByID's own patch map keys are interpolated directly into SQL
// text (identifiers can't be bound as parameters), and a patch map in
// particular is realistically built from less-trusted input (e.g. a
// decoded JSON request body) — validating here turns a crafted key into
// a clear error instead of a SQL-injection surface.
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

// mappedFields returns t's own db-tag-mapped fields, in declaration
// order — every exported field, skipping any tagged `db:"-"`. t must be
// a struct type (not a pointer).
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

// structColumnsAndValues reflects over record (a struct or pointer to
// struct) and returns its own mapped column names alongside the matching
// value for each, in the same order — what Insert/InsertReturning build
// their own column list and parameter values from.
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

// returningColumnsFor returns T's own mapped column names, in field
// order — what ExecReturning/InsertReturning request via opts.returning.
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
// own db-tag-mapped column names — the machinery ExecReturning/
// InsertReturning and Query/QueryReplica all share. ExecReturning/
// InsertReturning call it with cols already in T's own field order
// (returningColumnsFor[T]'s own output, the exact opts.returning it
// requested), since host.db.exec's ABI output carries no column_names to
// match against by name the way host.db.query's does — a caller-side
// detail of those two, not a requirement scanRow itself imposes; any
// column order works here as long as cols and row stay index-aligned
// with each other.
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

// scanRows populates one T per row via scanRow, all against the same
// cols — what Query/QueryReplica build their own []T result from
// host.db.query's rows/column_names.
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

// setFieldValue assigns raw (one value from a msgpack-decoded any[]
// row) into field, a struct field's own reflect.Value. field may be a
// pointer type for a nullable column (go-sdk-reference.md §6's own
// "pointer for nullable" convention) — raw == nil leaves a pointer field
// nil, matching database/sql.Scan's own precedent of rejecting NULL into
// anything else (a non-pointer field can't represent NULL, so silently
// zero-valuing it would make a real NULL indistinguishable from an
// actual empty/zero value).
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
			// conversion (e.g. int64 -> a named struct type) — restrict
			// conversion to the numeric kinds it's actually meant for.
		default:
			field.Set(rv.Convert(field.Type()))
			return nil
		}
	}
	return fmt.Errorf("cannot assign %s into %s", rv.Type(), field.Type())
}
