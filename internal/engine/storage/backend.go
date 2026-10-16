// Package storage provides object upload, download, deletion and signed URL access through
// the configured backend.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/djangbahevans/goerp/internal/engine/config"
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

func New(ctx context.Context, cfg config.Config) (Backend, error) {
	if err := cfg.ValidateStorage(); err != nil {
		return nil, err
	}

	switch cfg.StorageBackend {
	case "local":
		backend, err := NewLocal(cfg.StorageLocalDir)
		if err != nil {
			return nil, err
		}

		return backend, nil
	case "seaweedfs", "s3", "r2", "gcs":
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		backend, err := newS3CompatBackend(ctx, cfg)
		if err != nil {
			return nil, err
		}

		if _, err := backend.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: new(cfg.StorageBucket)}); err != nil {
			return nil, fmt.Errorf("check GOERP_STORAGE_BUCKET %q: %w", cfg.StorageBucket, err)
		}

		return backend, nil
	default:
		return nil, ErrUnknownBackend
	}
}
