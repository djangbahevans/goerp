package storage

import "errors"

var ErrS3NotSupported = errors.New("s3 storage backend not yet supported")

func newS3Backend() (Backend, error) {
	return nil, ErrS3NotSupported
}
