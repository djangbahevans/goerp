package storage

import "errors"

var ErrR2NotSupported = errors.New("r2 storage backend not yet supported")

func newR2Backend() (Backend, error) {
	return nil, ErrR2NotSupported
}
