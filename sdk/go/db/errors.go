package db

import (
	"errors"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Sentinel errors and type matchers for host.db.exec/exec_batch's own
// error codes (go-sdk-reference.md §6 "Database errors"). This package's
// write helpers (goerp#506) route their host call's error through
// wrapExecError before returning it.

var (
	// ErrNotFound is returned by QueryOne/QueryOneReplica (goerp#507) when
	// a query matches zero rows, and by ExecReturning/InsertReturning
	// when the statement itself matches zero rows (host.db.exec's own
	// db.no_rows_affected).
	ErrNotFound = errors.New("db: no matching row")

	// ErrEtagMismatch is returned by UpdateByID (goerp#506) when the row's
	// etag no longer matches what was read before the write —
	// host.db.exec's own db.etag_mismatch, translated into this package's
	// sentinel vocabulary. Carries no per-call detail (host.db.exec's own
	// etag-mismatch error has none of its own) — a plain sentinel, the
	// same shape as database/sql.ErrNoRows.
	ErrEtagMismatch = errors.New("db: etag mismatch (stale write)")
)

// PGError is a host.db.exec constraint-violation error's own structured
// detail (host-abi-reference.md §5's Details for db.unique_violation/
// db.foreign_key_violation), retrievable from any error this package's
// write helpers return via errors.As(err, &pgErr).
type PGError struct {
	// Code is Postgres's own SQLSTATE (e.g. "23505" for a unique
	// violation, "23503"/"23001" for a foreign-key violation) — not this
	// ABI's own "db.*" error code.
	Code           string
	ConstraintName string
	TableName      string
	ColumnName     string

	cause *hostcall.HostError
}

func (e *PGError) Error() string { return e.cause.Error() }

// Unwrap exposes the underlying *hostcall.HostError, so the Is*
// matchers below classify a *PGError the same way they'd classify the
// raw host error it wraps.
func (e *PGError) Unwrap() error { return e.cause }

// wrapExecError converts err — as returned by hostcall.Do for a
// host.db.exec/host.db.exec_batch call — into this package's own error
// vocabulary. A stale write becomes ErrEtagMismatch; a unique or
// foreign-key constraint violation becomes a *PGError carrying that
// error's own structured Details. Any other error (a different host.*
// failure, an error with no *hostcall.HostError anywhere in its chain, or
// nil) passes through unchanged.
func wrapExecError(err error) error {
	if err == nil {
		return nil
	}
	var he *hostcall.HostError
	if !errors.As(err, &he) {
		return err
	}
	switch he.Code {
	case "db.etag_mismatch":
		return ErrEtagMismatch
	case "db.no_rows_affected":
		return ErrNotFound
	case "db.unique_violation", "db.foreign_key_violation":
		return &PGError{
			Code:           detailString(he.Details, "sqlstate"),
			ConstraintName: detailString(he.Details, "constraint"),
			TableName:      detailString(he.Details, "table"),
			ColumnName:     detailString(he.Details, "column"),
			cause:          he,
		}
	}
	return err
}

func detailString(details map[string]any, key string) string {
	s, _ := details[key].(string)
	return s
}

// IsNotFound reports whether err is (or wraps) ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsEtagMismatch reports whether err is (or wraps) ErrEtagMismatch.
func IsEtagMismatch(err error) bool { return errors.Is(err, ErrEtagMismatch) }

// IsUniqueViolation reports whether err is a host.db.exec unique
// constraint violation — checked against the underlying
// *hostcall.HostError so it classifies both a raw host error and one
// already wrapped into *PGError identically.
func IsUniqueViolation(err error) bool {
	return hostErrorCodeIs(err, "db.unique_violation")
}

// IsForeignKeyViolation reports whether err is a host.db.exec
// foreign-key (or restrict) constraint violation.
func IsForeignKeyViolation(err error) bool {
	return hostErrorCodeIs(err, "db.foreign_key_violation")
}

// IsDeadlock reports whether err is a host.db.exec failure caused by a
// Postgres deadlock (SQLSTATE 40P01). host.db.exec doesn't special-case
// deadlocks the way it does unique/FK violations — they stay under the
// generic db.exec_error code — so this checks the error's own
// "sqlstate" Details field rather than the ABI code alone.
func IsDeadlock(err error) bool {
	var he *hostcall.HostError
	return errors.As(err, &he) && he.Code == "db.exec_error" && detailString(he.Details, "sqlstate") == "40P01"
}

func hostErrorCodeIs(err error, code string) bool {
	var he *hostcall.HostError
	return errors.As(err, &he) && he.Code == code
}
