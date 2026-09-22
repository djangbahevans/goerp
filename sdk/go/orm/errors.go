package orm

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// ErrNotFound is returned by ReadOne when id matches no record — a plain
// sentinel, the same shape as sdk/go/db's own ErrNotFound.
var ErrNotFound = errors.New("orm: no matching record")

// IsNotFound reports whether err is (or wraps) ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsEtagMismatch reports whether err is a host.orm.write failure caused
// by expectedEtag no longer matching the record's current etag —
// host.orm.write's own orm.etag_mismatch.
func IsEtagMismatch(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeEtagMismatch) }

// IsPreconditionFailed reports whether err is a host.orm.mutate failure
// caused by its Where guard being false for the record's current state —
// orm.precondition_failed.
func IsPreconditionFailed(err error) bool { return hostErrorCodeIs(err, abi.ErrCodePreconditionFailed) }

// IsValidationFailed reports whether err is a host.orm validation
// failure — orm.validation_failed. Some (not all) of these errors carry
// a "field" Details key, reachable via errors.As(err, &he).
func IsValidationFailed(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeValidationFailed) }

// IsUniqueViolation reports whether err is a host.orm.create/create_batch
// failure caused by a unique constraint — orm.unique_violation.
func IsUniqueViolation(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeUniqueViolation) }

// IsFieldNotWritable reports whether err is a host.orm.create/write/mutate
// failure caused by the caller supplying a value for a field no caller can
// write directly — a computed, readonly, or relation-owned field —
// orm.field_not_writable. A field the caller merely lacks permission to
// write is IsFieldWriteDenied.
func IsFieldNotWritable(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeFieldNotWritable) }

// IsFieldWriteDenied reports whether err is a host.orm.create/write/mutate
// failure caused by the caller lacking the write permission of a field
// declared OnDeniedWrite(model.Reject) — orm.field_write_denied. The
// offending field is in the error's Details["field"].
func IsFieldWriteDenied(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeFieldWriteDenied) }

// IsBatchTooLarge reports whether err is a create_batch/write_many/
// write_where/unlink failure caused by the call's records/IDs (or, for
// write_where, matched rows) exceeding GOERP_ORM_BULK_MAX_ROWS —
// orm.batch_too_large. No record is written when this fires. The limit
// and the requested count are in the error's Details["limit"]/["count"].
func IsBatchTooLarge(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeBatchTooLarge) }

// IsTimeout reports whether err is a host.orm failure caused by a
// statement of an ORM-owned transaction exceeding
// GOERP_ORM_STATEMENT_TIMEOUT — orm.timeout. The transaction is always
// rolled back when this fires; the same call may succeed on retry.
func IsTimeout(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeORMTimeout) }

func hostErrorCodeIs(err error, code string) bool {
	var he *abi.HostError
	return errors.As(err, &he) && he.Code == code
}
