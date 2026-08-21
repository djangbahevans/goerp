// Package storage provides the engine's object storage abstraction: a
// Backend interface backing GOERP_STORAGE_BACKEND, with a working local-disk
// implementation for development and stubs for seaweedfs/s3/r2/gcs that fail
// fast with a clear "not yet supported" error. See data-layer.md §6.1 for
// the canonical interface definition this mirrors, including what
// UploadOptions carries and why.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrUnknownBackend = errors.New("unknown storage backend")
)

type Backend interface {
	Upload(ctx context.Context, key string, r io.Reader, opts UploadOptions) (string, error)
	Download(ctx context.Context, fileID string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, fileID string) error
	DeleteByPrefix(ctx context.Context, prefix string) error
	SignedURL(ctx context.Context, fileID string, expiry time.Duration) (string, error)
	PublicURL(ctx context.Context, fileID string) (string, error)
	Exists(ctx context.Context, key string) (bool, error)
}

type UploadOptions struct {
	ContentType string
	Public      bool
}

func New(backendType string) (Backend, error) {
	switch backendType {
	case "local":
		return newLocalBackend()
	case "seaweedfs":
		return newSeaweedFsBackend()
	case "s3":
		return newS3Backend()
	case "r2":
		return newR2Backend()
	case "gcs":
		return newGCSBackend()
	default:
		return nil, ErrUnknownBackend
	}
}
