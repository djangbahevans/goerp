package db

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

// QueryResult is one host.db.query/query_replica response — Rows[i][j]
// corresponds to ColumnNames[j], matching host-abi-reference.md's own
// row/column-names wire shape rather than a per-row map, since a module
// caller typically wants either shape and building a map from this is
// one loop (see Rows.AsMaps below).
type QueryResult struct {
	Rows        [][]any  `msgpack:"rows"`
	ColumnNames []string `msgpack:"column_names"`
	DurationMs  float64  `msgpack:"duration_ms"`
}

// AsMaps converts Rows into one map[string]any per row, keyed by
// ColumnNames — the shape sdk/go/model.ProcessBatches hands to its own
// caller.
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

type dbQueryOpts struct {
	TimeoutMs int64 `msgpack:"timeout_ms"`
	ReadOnly  bool  `msgpack:"read_only"`
}

type dbQueryInput struct {
	SQL    string      `msgpack:"sql"`
	Params []any       `msgpack:"params"`
	TxID   string      `msgpack:"tx_id"`
	Opts   dbQueryOpts `msgpack:"opts"`
}

// QueryOption configures Query/QueryReplica — WithTimeout, WithReadOnly.
// Scoping a query to an open transaction is a tx.Query[T]/tx.QueryOne[T]
// method call instead (db.go) — there's no WithTx QueryOption here, so it
// doesn't collide with the package-level db.WithTx(fn) convenience
// wrapper (db.go).
type QueryOption func(*dbQueryInput)

// WithTimeout overrides host.db.query's default timeout.
func WithTimeout(ms int64) QueryOption {
	return func(in *dbQueryInput) { in.Opts.TimeoutMs = ms }
}

// WithReadOnly routes a Query call (one that would otherwise run against
// the primary) to a read replica instead, tolerating replica lag —
// host-abi-reference.md §5's opts.read_only. Meaningless on QueryReplica,
// which is already unconditionally replica-routed regardless of this
// option.
func WithReadOnly() QueryOption {
	return func(in *dbQueryInput) { in.Opts.ReadOnly = true }
}

// Query runs a parameterized SELECT via host.db.query — rejects anything
// but a single SELECT statement, engine-side (host-abi-reference.md §5) —
// and maps each returned row into a T via its own db-tag-mapped fields,
// the same struct-mapping conventions ExecReturning[T]/InsertReturning[T]
// use (reflect.go's mappedFields/scanRow[T]). params are positional
// ($1, $2, ...); the host substitutes them, never the module itself, so
// injection requires no caller discipline beyond not string-building the
// SQL itself.
func Query[T any](sql string, params []any, opts ...QueryOption) ([]T, error) {
	res, err := QueryRaw(sql, params, opts...)
	if err != nil {
		return nil, err
	}
	return scanRows[T](res.ColumnNames, res.Rows)
}

// QueryReplica is Query, but always routed to a read replica regardless
// of opts.read_only (host-abi-reference.md §5) — for read traffic that
// can tolerate replica lag and shouldn't add load to the primary.
func QueryReplica[T any](sql string, params []any, opts ...QueryOption) ([]T, error) {
	res, err := QueryReplicaRaw(sql, params, opts...)
	if err != nil {
		return nil, err
	}
	return scanRows[T](res.ColumnNames, res.Rows)
}

// QueryRaw is Query without the struct-tag-mapped []T layer — every
// returned row as its own positional []any, aligned against
// ColumnNames. Query[T] is the right choice for anything with a known
// result shape; QueryRaw is for a caller with no single T to map every
// row into instead (sdk/go/model.ProcessBatches, reading an arbitrary,
// caller-chosen table into map[string]any via QueryResult.AsMaps()).
func QueryRaw(sql string, params []any, opts ...QueryOption) (*QueryResult, error) {
	return query(hostDBQuery, sql, params, opts, "")
}

// QueryReplicaRaw is QueryRaw, but always routed to a read replica —
// QueryReplica's own counterpart to QueryRaw, the same way QueryReplica
// is Query's.
func QueryReplicaRaw(sql string, params []any, opts ...QueryOption) (*QueryResult, error) {
	return query(hostDBQueryReplica, sql, params, opts, "")
}

// query is Query/QueryReplica's shared implementation, plus the
// transaction-scoped path (tx *Tx) Query[T]/QueryOne[T] (db.go) call it
// through by passing tx's own ID as txID instead of "".
func query(invoke hostcall.Invoke, sql string, params []any, opts []QueryOption, txID string) (*QueryResult, error) {
	in := dbQueryInput{SQL: sql, Params: params, TxID: txID}
	for _, opt := range opts {
		opt(&in)
	}

	var out QueryResult
	if err := hostcall.Do(invoke, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// firstRow returns res's own first row scanned into a T, or ErrNotFound
// if res carries no rows — QueryOne/QueryOneReplica/tx.QueryOne's shared
// zero-or-one-row interpretation of an already-fetched QueryResult.
func firstRow[T any](res *QueryResult) (T, error) {
	var zero T
	if len(res.Rows) == 0 {
		return zero, ErrNotFound
	}
	return scanRow[T](res.ColumnNames, res.Rows[0])
}
