// Package db is the module-side caller for the host.db namespace
// (host-abi-reference.md §5): Begin/Commit/Rollback for a transaction a
// module opens and does work under (e.g. sdk/go/events.EmitTx),
// Query/QueryReplica for read-only SELECTs (query.go), Exec/ExecReturning/
// ExecBatch/Insert/InsertReturning/UpdateByID for writes
// (exec.go/exec_batch.go/insert.go/update.go, go-sdk-reference.md §6
// "Exec — write rows"), Lock/TryLock for tenant-scoped Postgres advisory
// locking (lock.go), Notify for tenant-scoped Postgres NOTIFY (notify.go),
// NewQuery/NewPatch for hand-assembling dynamic SQL (query_builder.go/
// patch.go, go-sdk-reference.md section 6 "Query builder"/"Patch builder"),
// and this package's own sentinel errors/type matchers for classifying a
// host.db.exec-originated failure (errors.go).
package db

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

// Tx is a handle to a transaction opened via host.db.begin. Its zero
// value is not usable — obtain one from Begin.
type Tx struct {
	id        string
	committed bool
}

// TxID returns the underlying host transaction ID. Exposed for other
// sdk/go packages (e.g. sdk/go/events.EmitTx) that need to reference this
// transaction in their own host calls — treat the value itself as
// opaque.
func (tx *Tx) TxID() string { return tx.id }

type dbBeginInput struct {
	Isolation string `msgpack:"isolation"`
	ReadOnly  bool   `msgpack:"read_only"`
}

type dbBeginOutput struct {
	TxID      string `msgpack:"tx_id"`
	ExpiresAt int64  `msgpack:"expires_at"`
}

type dbTxIDInput struct {
	TxID string `msgpack:"tx_id"`
}

type dbDurationOutput struct {
	DurationMs float64 `msgpack:"duration_ms"`
}

// BeginOption configures Begin — WithIsolation, ReadOnly.
type BeginOption func(*dbBeginInput)

// WithIsolation sets the transaction's SQL isolation level (e.g.
// "serializable"). Unset uses the database's default.
func WithIsolation(level string) BeginOption {
	return func(in *dbBeginInput) { in.Isolation = level }
}

// ReadOnly marks the transaction read-only.
func ReadOnly() BeginOption {
	return func(in *dbBeginInput) { in.ReadOnly = true }
}

// Begin opens a new transaction via host.db.begin.
func Begin(opts ...BeginOption) (*Tx, error) {
	var in dbBeginInput
	for _, opt := range opts {
		opt(&in)
	}

	var out dbBeginOutput
	if err := hostcall.Do(hostDBBegin, in, &out); err != nil {
		return nil, err
	}
	return &Tx{id: out.TxID}, nil
}

// Commit commits tx via host.db.commit.
func (tx *Tx) Commit() error {
	if tx.committed {
		return nil
	}

	var out dbDurationOutput
	if err := hostcall.Do(hostDBCommit, dbTxIDInput{TxID: tx.id}, &out); err != nil {
		return err
	}
	tx.committed = true
	return nil
}

// Rollback rolls tx back via host.db.rollback. Safe to call after
// Commit — a no-op, matching go-sdk-reference.md §6.
func (tx *Tx) Rollback() error {
	if tx.committed {
		return nil
	}

	var out dbDurationOutput
	return hostcall.Do(hostDBRollback, dbTxIDInput{TxID: tx.id}, &out)
}

// Query is Query, scoped to tx's own open transaction — a generic
// method (Go 1.27+) rather than a QueryTx-suffixed free function, since
// Tx and this method are both defined in db itself (go-sdk-reference.md
// §6 "Transactions").
func (tx *Tx) Query[T any](sql string, params []any, opts ...QueryOption) ([]T, error) {
	res, err := query(hostDBQuery, sql, params, opts, tx.id)
	if err != nil {
		return nil, err
	}
	return scanRows[T](res.ColumnNames, res.Rows)
}

// QueryOne is QueryOne, scoped to tx's own open transaction.
func (tx *Tx) QueryOne[T any](sql string, params []any, opts ...QueryOption) (T, error) {
	var zero T
	res, err := query(hostDBQuery, sql, params, opts, tx.id)
	if err != nil {
		return zero, err
	}
	return firstRow[T](res)
}

// WithTx runs fn inside a new transaction opened via Begin: a nil return
// commits, any other return (or a panic) rolls back instead, propagating
// fn's own error or panic unchanged. The deferred Rollback also covers a
// failed Commit — Commit only marks tx committed on success, so a
// deferred Rollback after a failed Commit still runs for real instead of
// leaving the transaction open on the engine until it expires.
func WithTx(fn func(tx *Tx) error) error {
	tx, err := Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
