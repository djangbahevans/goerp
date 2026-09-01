package orm

import (
	"errors"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// ErrNotFound is returned by ReadOne when id matches no record — a plain
// sentinel, the same shape as sdk/go/db's own ErrNotFound.
var ErrNotFound = errors.New("orm: no matching record")

// IsNotFound reports whether err is (or wraps) ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsEtagMismatch reports whether err is a host.orm.write failure caused
// by expectedEtag no longer matching the record's current etag —
// host.orm.write's own orm.etag_mismatch.
func IsEtagMismatch(err error) bool { return hostErrorCodeIs(err, "orm.etag_mismatch") }

// IsValidationFailed reports whether err is a host.orm validation
// failure — orm.validation_failed. The field name is reachable via
// errors.As(err, &he); he.Details["field"].
func IsValidationFailed(err error) bool { return hostErrorCodeIs(err, "orm.validation_failed") }

// IsUniqueViolation reports whether err is a host.orm.create/create_batch
// failure caused by a unique constraint — orm.unique_violation.
func IsUniqueViolation(err error) bool { return hostErrorCodeIs(err, "orm.unique_violation") }

// IsFieldNotWritable reports whether err is a host.orm.create/write
// failure caused by the caller supplying a value for a computed,
// relation-owned, or otherwise non-writable field — orm.field_not_writable.
func IsFieldNotWritable(err error) bool { return hostErrorCodeIs(err, "orm.field_not_writable") }

func hostErrorCodeIs(err error, code string) bool {
	var he *hostcall.HostError
	return errors.As(err, &he) && he.Code == code
}
