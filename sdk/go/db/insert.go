package db

import (
	"fmt"
	"strings"
)

// Insert builds and executes a parameterized INSERT into table from
// record's own db-tag-mapped fields (reflect.go's structColumnsAndValues
// — the mirror image of ExecReturning's own tag-driven column mapping).
func Insert(table string, record any) error {
	sql, vals, err := buildInsertSQL(table, record)
	if err != nil {
		return err
	}
	_, err = Exec(sql, vals...)
	return err
}

// InsertReturning is Insert, but requests T's own db-mapped columns back
// via RETURNING and unmarshals the inserted row into a new T — the same
// column-alignment approach ExecReturning uses.
func InsertReturning[T any](table string, record any) (T, error) {
	var zero T
	sql, vals, err := buildInsertSQL(table, record)
	if err != nil {
		return zero, err
	}

	returningCols, err := returningColumnsFor[T]()
	if err != nil {
		return zero, err
	}
	if len(returningCols) == 0 {
		return zero, fmt.Errorf("db: %T has no db-mapped fields to return", zero)
	}
	return scanOneReturning[T](dbExecInput{
		SQL: sql, Params: vals,
		Opts: dbExecOpts{Returning: strings.Join(returningCols, ","), ExpectRows: true},
	}, returningCols)
}

func buildInsertSQL(table string, record any) (sql string, vals []any, err error) {
	if err := validateIdentifier(table); err != nil {
		return "", nil, err
	}
	cols, vals, err := structColumnsAndValues(record)
	if err != nil {
		return "", nil, err
	}
	if len(cols) == 0 {
		return "", nil, fmt.Errorf("db: %T has no db-mapped fields to insert", record)
	}

	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	return sql, vals, nil
}
