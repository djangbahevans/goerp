package db

import (
	"fmt"
	"strings"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// ExecResult is the outcome of an Exec call.
type ExecResult struct {
	RowsAffected int64
	DurationMs   float64
}

// Exec executes a parameterized INSERT/UPDATE/DELETE via host.db.exec.
func Exec(sql string, args ...any) (ExecResult, error) {
	return exec(abi.DBExecInput{SQL: sql, Params: args})
}

// Exec is Exec, scoped to tx's open transaction.
func (tx *Tx) Exec(sql string, args ...any) (ExecResult, error) {
	return exec(abi.DBExecInput{SQL: sql, Params: args, TxID: tx.id})
}

func exec(in abi.DBExecInput) (ExecResult, error) {
	var out abi.DBExecOutput
	if err := hostcall.Do(hostDBExec, in, &out); err != nil {
		return ExecResult{}, wrapExecError(err)
	}
	return ExecResult{RowsAffected: int64(out.RowsAffected), DurationMs: out.DurationMs}, nil
}

// ExecReturning executes sql (an INSERT/UPDATE/DELETE), returns T's
// db-tag-mapped columns, and unmarshals the single affected row into a new
// T. Returns ErrNotFound if the statement affected no rows.
func ExecReturning[T any](sql string, args ...any) (T, error) {
	return execReturning[T](sql, args, "")
}

// ExecReturning is ExecReturning, scoped to tx's open transaction.
func (tx *Tx) ExecReturning[T any](sql string, args ...any) (T, error) {
	return execReturning[T](sql, args, tx.id)
}

func execReturning[T any](sql string, args []any, txID string) (T, error) {
	var zero T
	cols, err := returningColumnsFor[T]()
	if err != nil {
		return zero, err
	}
	if len(cols) == 0 {
		return zero, fmt.Errorf("db: %T has no db-mapped fields to return", zero)
	}
	return scanOneReturning[T](abi.DBExecInput{
		SQL: sql, Params: args, TxID: txID,
		Opts: abi.DBExecOpts{Returning: strings.Join(cols, ","), ExpectRows: true},
	}, cols)
}

// scanOneReturning executes in, which must already request cols via
// opts.returning with expect_rows set, and scans the single affected row
// into a new T. The exec output has no column names, so row values align
// with cols by position.
func scanOneReturning[T any](in abi.DBExecInput, cols []string) (T, error) {
	var zero T
	var out abi.DBExecOutput
	if err := hostcall.Do(hostDBExec, in, &out); err != nil {
		return zero, wrapExecError(err)
	}
	if len(out.Returning) == 0 {
		// expect_rows makes the host report zero rows as an error; this
		// guards against that invariant breaking.
		return zero, fmt.Errorf("db: host.db.exec returned no rows despite expect_rows")
	}
	return scanRow[T](cols, out.Returning[0])
}
