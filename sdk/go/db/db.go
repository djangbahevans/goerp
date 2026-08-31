// Package db is the module-side caller for the host.db namespace
// (host-abi-reference.md §5): Begin/Commit/Rollback for a transaction a
// module opens and does work under (e.g. sdk/go/events.EmitTx),
// Query/QueryReplica for read-only SELECTs (query.go), Exec/ExecTx/
// ExecReturning/ExecBatch/Insert/InsertReturning/UpdateByID for writes
// (exec.go/exec_batch.go/insert.go/update.go, go-sdk-reference.md §6
// "Exec — write rows"), and this package's own sentinel errors/type
// matchers for classifying a host.db.exec-originated failure
// (errors.go).
//
// host.db.lock/notify are documented in host-abi-reference.md §5 but not
// yet implemented engine-side, so db.Lock and friends aren't buildable
// yet either. QueryOne/QueryPaged/Exists/Count/WithTx/QueryTx/
// QueryOneTx/ExecReturningTx (go-sdk-reference.md §6's remaining
// read/transaction helpers) haven't landed yet either (goerp#507).
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
