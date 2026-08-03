package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type LocalBackend struct {
	dir string
}

func newLocalBackend() (*LocalBackend, error) {
	dir := os.Getenv("GOERP_STORAGE_LOCAL_DIR")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}

	return &LocalBackend{dir}, nil
}

func (b *LocalBackend) Upload(ctx context.Context, key string, r io.Reader, opts UploadOptions) (string, error) {
	dest := filepath.Join(b.dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("create parent directory: %w", err)
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, r); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close file: %w", err)
	}

	return dest, nil
}

func (b *LocalBackend) Download(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	f := filepath.Join(b.dir, filepath.FromSlash(key))

	r, err := os.Open(f)
	if err != nil {
		return nil, 0, fmt.Errorf("open file: %w", err)
	}

	fileinfo, err := os.Stat(f)
	if err != nil {
		return nil, 0, fmt.Errorf("stat file: %w", err)
	}

	return r, fileinfo.Size(), err
}

func (b *LocalBackend) Delete(ctx context.Context, key string) error {
	return os.Remove(filepath.Join(b.dir, filepath.FromSlash(key)))
}

func (b *LocalBackend) SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return b.localURL(key), nil
}

func (b *LocalBackend) PublicURL(ctx context.Context, key string) (string, error) {
	return b.localURL(key), nil
}

func (b *LocalBackend) Exists(ctx context.Context, key string) (bool, error) {
	_, err := os.Stat(filepath.Join(b.dir, filepath.FromSlash(key)))
	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, err
}

func (b *LocalBackend) localURL(key string) string {
	abs, err := filepath.Abs(filepath.Join(b.dir, filepath.FromSlash(key)))
	if err != nil {
		return ""
	}

	return (&url.URL{Scheme: "file", Path: abs}).String()
}
