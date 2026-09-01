package db

// QueryOne is Query, but returns exactly one row instead of a slice —
// ErrNotFound if sql matched zero rows, the same convention
// ExecReturning uses for a write that matched none.
func QueryOne[T any](sql string, params []any, opts ...QueryOption) (T, error) {
	var zero T
	res, err := query(hostDBQuery, sql, params, opts, "")
	if err != nil {
		return zero, err
	}
	return firstRow[T](res)
}

// QueryOneReplica is QueryOne, but always routed to a read replica —
// QueryReplica's own counterpart to Query, applied to QueryOne.
func QueryOneReplica[T any](sql string, params []any, opts ...QueryOption) (T, error) {
	var zero T
	res, err := query(hostDBQueryReplica, sql, params, opts, "")
	if err != nil {
		return zero, err
	}
	return firstRow[T](res)
}
