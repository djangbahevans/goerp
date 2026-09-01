package db

import "fmt"

// existsRow maps SELECT EXISTS(...)'s own single boolean column — named
// "exists" by Postgres for a bare EXISTS(...) expression.
type existsRow struct {
	Exists bool `db:"exists"`
}

// countRow maps SELECT count(*)'s own single column — named "count" by
// Postgres for a bare count(*) expression.
type countRow struct {
	Count int64 `db:"count"`
}

// Exists reports whether any row in table matches condition — a raw SQL
// boolean expression referencing args positionally ($1, $2, ...), the
// same convention Query's own WHERE clauses use.
func Exists(table, condition string, args ...any) (bool, error) {
	if err := validateIdentifier(table); err != nil {
		return false, err
	}
	sql := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE %s) AS "exists"`, table, condition)
	row, err := QueryOne[existsRow](sql, args)
	if err != nil {
		return false, err
	}
	return row.Exists, nil
}

// Count returns how many rows in table match condition — a raw SQL
// boolean expression referencing args positionally ($1, $2, ...).
func Count(table, condition string, args ...any) (int64, error) {
	if err := validateIdentifier(table); err != nil {
		return 0, err
	}
	sql := fmt.Sprintf("SELECT count(*) AS count FROM %s WHERE %s", table, condition)
	row, err := QueryOne[countRow](sql, args)
	if err != nil {
		return 0, err
	}
	return row.Count, nil
}
