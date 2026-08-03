package storage

import "errors"

var ErrGCSNotSupported = errors.New("gcs storage backend not yet supported")

func newGCSBackend() (Backend, error) {
	return nil, ErrGCSNotSupported
}
