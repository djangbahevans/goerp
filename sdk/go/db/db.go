// Package db runs SQL against the tenant's database through the engine's
// host.db calls: Begin/Commit/Rollback for transactions, Query and
// QueryReplica for SELECTs, Exec, ExecReturning, ExecBatch, Insert,
// InsertReturning and UpdateByID for writes, Lock/TryLock for advisory
// locks, Notify for Postgres NOTIFY, and NewQuery/NewPatch for building
// dynamic SQL. Prefer sdk/go/orm for model records; it applies access
// rules that raw SQL bypasses.
package db

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Tx is a handle to an open transaction. Obtain one from Begin or receive
// an engine-managed transaction in a transactional event handler. Its zero
// value is not usable.
type Tx struct {
	id        string
	committed bool
	managed   bool
}

// TxID returns the opaque host transaction ID other SDK packages use to
// join this transaction.
func (tx *Tx) TxID() string { return tx.id }

// NewManagedTx joins an engine-managed transaction using its nonempty ID.
// It does not open or validate the transaction. The handle is valid only
// during the invocation that receives the ID; Commit and Rollback return
// db.transaction_managed errors because the engine owns its outcome.
func NewManagedTx(id string) (*Tx, error) {
	if id == "" {
		return nil, errors.New("db: engine-managed transaction ID is required")
	}

	return &Tx{id: id, managed: true}, nil
}

// BeginOption configures Begin — WithIsolation, ReadOnly.
type BeginOption func(*abi.DBBeginInput)

// WithIsolation sets the transaction's SQL isolation level (e.g.
// "serializable"). Unset uses the database's default.
func WithIsolation(level string) BeginOption {
	return func(in *abi.DBBeginInput) { in.Isolation = level }
}

// ReadOnly marks the transaction read-only.
func ReadOnly() BeginOption {
	return func(in *abi.DBBeginInput) { in.ReadOnly = true }
}

// Begin opens a new transaction via host.db.begin.
func Begin(opts ...BeginOption) (*Tx, error) {
	var in abi.DBBeginInput
	for _, opt := range opts {
		opt(&in)
	}

	var out abi.DBBeginOutput
	if err := hostcall.Do(hostDBBegin, in, &out); err != nil {
		return nil, err
	}
	return &Tx{id: out.TxID}, nil
}

// Commit commits a transaction opened by Begin. It returns a
// db.transaction_managed error for an engine-managed transaction.
func (tx *Tx) Commit() error {
	if tx.managed {
		return &abi.HostError{Code: abi.ErrCodeTransactionManaged, Message: "the engine owns this transaction"}
	}

	if tx.committed {
		return nil
	}

	var out abi.DBDurationOutput
	if err := hostcall.Do(hostDBCommit, abi.DBTxIDInput{TxID: tx.id}, &out); err != nil {
		return err
	}
	tx.committed = true
	return nil
}

// Rollback rolls back a transaction opened by Begin. After a successful
// Commit it is a no-op. It returns a db.transaction_managed error for an
// engine-managed transaction.
func (tx *Tx) Rollback() error {
	if tx.managed {
		return &abi.HostError{Code: abi.ErrCodeTransactionManaged, Message: "the engine owns this transaction"}
	}

	if tx.committed {
		return nil
	}

	var out abi.DBDurationOutput
	return hostcall.Do(hostDBRollback, abi.DBTxIDInput{TxID: tx.id}, &out)
}

// Query is Query, scoped to tx's open transaction.
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

// WithTx runs fn inside a new transaction: a nil return commits, any other
// return or a panic rolls back and is propagated unchanged. A failed
// Commit is also rolled back.
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
