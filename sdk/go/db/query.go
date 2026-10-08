package db

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// QueryResult is the outcome of a raw query: Rows[i][j] is the value of
// column ColumnNames[j]. AsMaps converts it to one map per row.
type QueryResult struct {
	Rows        [][]any
	ColumnNames []string
	DurationMs  float64
}

// AsMaps converts Rows into one map[string]any per row, keyed by
// ColumnNames.
func (r *QueryResult) AsMaps() []map[string]any {
	maps := make([]map[string]any, len(r.Rows))
	for i, row := range r.Rows {
		m := make(map[string]any, len(r.ColumnNames))
		for j, col := range r.ColumnNames {
			if j < len(row) {
				m[col] = row[j]
			}
		}
		maps[i] = m
	}
	return maps
}

// QueryOption configures Query/QueryReplica. To query inside a
// transaction, call tx.Query or tx.QueryOne.
type QueryOption func(*abi.DBQueryInput)

// WithTimeout overrides host.db.query's default timeout.
func WithTimeout(ms int64) QueryOption {
	return func(in *abi.DBQueryInput) { in.Opts.TimeoutMs = ms }
}

// WithReadOnly routes a Query call to a read replica, tolerating replica
// lag. QueryReplica always uses a replica, so it ignores this option.
func WithReadOnly() QueryOption {
	return func(in *abi.DBQueryInput) { in.Opts.ReadOnly = true }
}

// Query runs a single parameterized SELECT and maps each row into a T by
// its db-tagged fields; the engine rejects any other statement. params
// bind to $1, $2, ... on the host, so values never need escaping as long
// as sql itself isn't built from input.
func Query[T any](sql string, params []any, opts ...QueryOption) ([]T, error) {
	res, err := QueryRaw(sql, params, opts...)
	if err != nil {
		return nil, err
	}
	return scanRows[T](res.ColumnNames, res.Rows)
}

// QueryReplica is Query, always routed to a read replica: for reads that
// tolerate replica lag and shouldn't load the primary.
func QueryReplica[T any](sql string, params []any, opts ...QueryOption) ([]T, error) {
	res, err := QueryReplicaRaw(sql, params, opts...)
	if err != nil {
		return nil, err
	}
	return scanRows[T](res.ColumnNames, res.Rows)
}

// QueryRaw is Query without struct mapping: each row is a positional []any
// aligned with ColumnNames. Use it when the result has no fixed shape.
func QueryRaw(sql string, params []any, opts ...QueryOption) (*QueryResult, error) {
	return query(hostDBQuery, sql, params, opts, "")
}

// QueryReplicaRaw is QueryRaw, always routed to a read replica.
func QueryReplicaRaw(sql string, params []any, opts ...QueryOption) (*QueryResult, error) {
	return query(hostDBQueryReplica, sql, params, opts, "")
}

// query implements Query, QueryReplica and the Tx methods; txID is "" outside
// a transaction.
func query(invoke hostcall.Invoke, sql string, params []any, opts []QueryOption, txID string) (*QueryResult, error) {
	in := abi.DBQueryInput{SQL: sql, Params: params, TxID: txID}
	for _, opt := range opts {
		opt(&in)
	}

	var out abi.DBQueryOutput
	if err := hostcall.Do(invoke, in, &out); err != nil {
		return nil, err
	}
	return &QueryResult{Rows: out.Rows, ColumnNames: out.ColumnNames, DurationMs: out.DurationMs}, nil
}

// firstRow returns res's first row scanned into a T, or ErrNotFound if res
// has no rows.
func firstRow[T any](res *QueryResult) (T, error) {
	var zero T
	if len(res.Rows) == 0 {
		return zero, ErrNotFound
	}
	return scanRow[T](res.ColumnNames, res.Rows[0])
}
