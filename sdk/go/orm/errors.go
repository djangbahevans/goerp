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
func IsEtagMismatch(err error) bool {
	var he *hostcall.HostError
	return errors.As(err, &he) && he.Code == "orm.etag_mismatch"
}
