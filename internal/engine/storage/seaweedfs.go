package storage

import "errors"

var ErrSeaweedFsNotSupported = errors.New("seaweedfs storage backend not yet supported")

func newSeaweedFsBackend() (Backend, error) {
	return nil, ErrSeaweedFsNotSupported
}
