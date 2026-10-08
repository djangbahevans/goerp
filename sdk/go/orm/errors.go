package orm

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// ErrNotFound is returned when a requested id matches no record.
var ErrNotFound = errors.New("orm: no matching record")

// IsNotFound reports whether err is (or wraps) ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsEtagMismatch reports whether err is a write failure caused by the
// expected etag not matching the record's current etag
// (orm.etag_mismatch).
func IsEtagMismatch(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeEtagMismatch) }

// IsPreconditionFailed reports whether err is a Mutate failure caused by
// its Where guard being false for the record's current state
// (orm.precondition_failed).
func IsPreconditionFailed(err error) bool { return hostErrorCodeIs(err, abi.ErrCodePreconditionFailed) }

// IsValidationFailed reports whether err is a validation failure
// (orm.validation_failed). Some of these errors name the offending field in
// the "field" Details key, reachable via errors.AsType[*abi.HostError](err).
func IsValidationFailed(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeValidationFailed) }

// IsUniqueViolation reports whether err is a Create or CreateBatch failure
// caused by a unique constraint (orm.unique_violation).
func IsUniqueViolation(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeUniqueViolation) }

// IsFieldNotWritable reports whether err is a create, write or mutate
// failure caused by a value for a field no caller can write directly: a
// computed, readonly or relation-owned field (orm.field_not_writable). A
// field the caller lacks permission to write is IsFieldWriteDenied.
func IsFieldNotWritable(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeFieldNotWritable) }

// IsFieldWriteDenied reports whether err is a create, write or mutate
// failure caused by the caller lacking the write permission of a field
// declared OnDeniedWrite(model.Reject) (orm.field_write_denied). The
// offending field is in the error's Details["field"].
func IsFieldWriteDenied(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeFieldWriteDenied) }

// IsBatchTooLarge reports whether err is a CreateBatch, WriteMany,
// WriteWhere or Unlink failure caused by the records, IDs or matched rows
// exceeding the engine's GOERP_ORM_BULK_MAX_ROWS limit
// (orm.batch_too_large). No record is written. The limit and requested
// count are in the error's Details["limit"] and Details["count"].
func IsBatchTooLarge(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeBatchTooLarge) }

// IsTimeout reports whether err is caused by a statement exceeding the
// engine's GOERP_ORM_STATEMENT_TIMEOUT (orm.timeout). The transaction is
// rolled back; the same call may succeed on retry.
func IsTimeout(err error) bool { return hostErrorCodeIs(err, abi.ErrCodeORMTimeout) }

func hostErrorCodeIs(err error, code string) bool {
	he, ok := errors.AsType[*abi.HostError](err)
	return ok && he.Code == code
}
