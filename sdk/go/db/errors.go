package db

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// Sentinel errors and type matchers for database errors. This package's
// write helpers return errors already classified by wrapExecError.

var (
	// ErrNotFound is returned by QueryOne/QueryOneReplica when a query
	// matches zero rows, and by ExecReturning/InsertReturning when the
	// statement affects zero rows.
	ErrNotFound = errors.New("db: no matching row")

	// ErrEtagMismatch is returned by UpdateByID when the row's etag
	// differs from the one read before the write.
	ErrEtagMismatch = errors.New("db: etag mismatch (stale write)")
)

// PGError is the structured detail of a db.unique_violation or
// db.foreign_key_violation error, retrievable from any error this
// package's write helpers return via errors.AsType[*db.PGError](err).
type PGError struct {
	// Code is the Postgres SQLSTATE (e.g. "23505" for a unique
	// violation, "23503"/"23001" for a foreign-key violation), not a
	// "db.*" error code.
	Code           string
	ConstraintName string
	TableName      string
	ColumnName     string

	cause *abi.HostError
}

func (e *PGError) Error() string { return e.cause.Error() }

// Unwrap exposes the underlying *abi.HostError, so the Is*
// matchers below classify a *PGError the same way they'd classify the
// raw host error it wraps.
func (e *PGError) Unwrap() error { return e.cause }

// wrapExecError classifies a host.db.exec or exec_batch error: a stale
// write becomes ErrEtagMismatch and a unique or foreign-key violation
// becomes a *PGError. Any other error, or nil, passes through unchanged.
func wrapExecError(err error) error {
	if err == nil {
		return nil
	}
	he, ok := errors.AsType[*abi.HostError](err)
	if !ok {
		return err
	}
	switch he.Code {
	case abi.ErrCodeDBEtagMismatch:
		return ErrEtagMismatch
	case abi.ErrCodeNoRowsAffected:
		return ErrNotFound
	case abi.ErrCodeDBUniqueViolation, abi.ErrCodeDBForeignKeyViolation:
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

// IsUniqueViolation reports whether err is a unique constraint violation.
func IsUniqueViolation(err error) bool {
	return hostErrorCodeIs(err, abi.ErrCodeDBUniqueViolation)
}

// IsForeignKeyViolation reports whether err is a foreign-key or restrict
// constraint violation.
func IsForeignKeyViolation(err error) bool {
	return hostErrorCodeIs(err, abi.ErrCodeDBForeignKeyViolation)
}

// IsDeadlock reports whether err is caused by a Postgres deadlock
// (SQLSTATE 40P01). Deadlocks arrive as a generic db.exec_error, so this
// checks the error's "sqlstate" detail.
func IsDeadlock(err error) bool {
	he, ok := errors.AsType[*abi.HostError](err)
	return ok && he.Code == abi.ErrCodeExecError && detailString(he.Details, "sqlstate") == "40P01"
}

func hostErrorCodeIs(err error, code string) bool {
	he, ok := errors.AsType[*abi.HostError](err)
	return ok && he.Code == code
}
