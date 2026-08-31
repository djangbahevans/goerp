package db

import (
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// ExecResult is host.db.exec's own response — go-sdk-reference.md §6
// "Exec — write rows".
type ExecResult struct {
	RowsAffected int64
	DurationMs   float64
}

type dbExecOpts struct {
	Returning  string `msgpack:"returning,omitempty"`
	ExpectRows bool   `msgpack:"expect_rows,omitempty"`
}

type dbExecInput struct {
	SQL    string     `msgpack:"sql"`
	Params []any      `msgpack:"params"`
	TxID   string     `msgpack:"tx_id,omitempty"`
	Opts   dbExecOpts `msgpack:"opts"`
}

type dbExecOutput struct {
	RowsAffected int     `msgpack:"rows_affected"`
	Returning    [][]any `msgpack:"returning,omitempty"`
	DurationMs   float64 `msgpack:"duration_ms"`
}

// Exec executes a parameterized INSERT/UPDATE/DELETE via host.db.exec.
func Exec(sql string, args ...any) (ExecResult, error) {
	return exec(dbExecInput{SQL: sql, Params: args})
}

// ExecTx is Exec, scoped to tx's own open transaction.
func ExecTx(tx *Tx, sql string, args ...any) (ExecResult, error) {
	return exec(dbExecInput{SQL: sql, Params: args, TxID: tx.TxID()})
}

func exec(in dbExecInput) (ExecResult, error) {
	var out dbExecOutput
	if err := hostcall.Do(hostDBExec, in, &out); err != nil {
		return ExecResult{}, wrapExecError(err)
	}
	return ExecResult{RowsAffected: int64(out.RowsAffected), DurationMs: out.DurationMs}, nil
}

// ExecReturning executes sql (an INSERT/UPDATE/DELETE), requesting T's own
// db-tag-mapped columns via opts.returning (in T's own field order — the
// only way to align a returned row back to T's fields, since
// host.db.exec's own ABI output carries no column_names the way
// host.db.query's does), and unmarshals the single matched row into a
// new T. Returns ErrNotFound if the statement matched no rows.
func ExecReturning[T any](sql string, args ...any) (T, error) {
	var zero T
	cols, err := returningColumnsFor[T]()
	if err != nil {
		return zero, err
	}
	if len(cols) == 0 {
		return zero, fmt.Errorf("db: %T has no db-mapped fields to return", zero)
	}
	return scanOneReturning[T](dbExecInput{
		SQL: sql, Params: args,
		Opts: dbExecOpts{Returning: strings.Join(cols, ","), ExpectRows: true},
	}, cols)
}

// scanOneReturning executes in — which must already carry
// opts.returning/expect_rows set from cols — and scans the single
// matched row into a new T, aligned against cols. Shared by
// ExecReturning and InsertReturning (insert.go).
func scanOneReturning[T any](in dbExecInput, cols []string) (T, error) {
	var zero T
	var out dbExecOutput
	if err := hostcall.Do(hostDBExec, in, &out); err != nil {
		return zero, wrapExecError(err)
	}
	if len(out.Returning) == 0 {
		// expect_rows should already have turned a zero-row match into
		// db.no_rows_affected (wrapExecError's own ErrNotFound above) —
		// this is a defensive guard against that invariant ever breaking,
		// not a path expected to run.
		return zero, fmt.Errorf("db: host.db.exec returned no rows despite expect_rows")
	}
	return scanRow[T](cols, out.Returning[0])
}
