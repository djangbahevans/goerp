package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func newTestLocalBackend(t *testing.T) *LocalBackend {
	t.Helper()

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, newErr := newLocalBackend()
	if newErr != nil {
		t.Fatalf("newLocalBackend() error: %v", newErr)
	}
	return backend
}

func TestLocalBackendUploadDownload(t *testing.T) {
	b := newTestLocalBackend(t)
	ctx := context.Background()
	content := []byte("hello, storage")

	key, err := b.Upload(ctx, "attachments/demo.txt", bytes.NewReader(content), UploadOptions{ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("Upload() error: %v", err)
	}

	r, size, err := b.Download(ctx, "attachments/demo.txt")
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}
	defer func() { _ = r.Close() }()

	if size != int64(len(content)) {
		t.Errorf("Download() size = %d, want %d", size, len(content))
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read downloaded content: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("downloaded content = %q, want %q", got, content)
	}

	if !strings.HasSuffix(key, "attachments/demo.txt") && !strings.HasSuffix(key, `attachments\demo.txt`) {
		t.Errorf("Upload() returned key %q, want it to reference attachments/demo.txt", key)
	}
}

func TestLocalBackendDownloadMissing(t *testing.T) {
	b := newTestLocalBackend(t)

	_, _, err := b.Download(context.Background(), "does/not/exist.txt")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Download() of a missing key error = %v, want a wrapped os.ErrNotExist", err)
	}
}

func TestLocalBackendDelete(t *testing.T) {
	b := newTestLocalBackend(t)
	ctx := context.Background()

	if _, err := b.Upload(ctx, "to-delete.txt", strings.NewReader("bye"), UploadOptions{}); err != nil {
		t.Fatalf("Upload() error: %v", err)
	}

	if err := b.Delete(ctx, "to-delete.txt"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	if _, _, err := b.Download(ctx, "to-delete.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Download() after Delete() error = %v, want os.ErrNotExist", err)
	}
}

func TestLocalBackendDeleteByPrefix(t *testing.T) {
	b := newTestLocalBackend(t)
	ctx := context.Background()

	if _, err := b.Upload(ctx, "tenant-acme/files/a.txt", strings.NewReader("a"), UploadOptions{}); err != nil {
		t.Fatalf("Upload() error: %v", err)
	}
	if _, err := b.Upload(ctx, "tenant-acme/files/b.txt", strings.NewReader("b"), UploadOptions{}); err != nil {
		t.Fatalf("Upload() error: %v", err)
	}
	if _, err := b.Upload(ctx, "tenant-other/c.txt", strings.NewReader("c"), UploadOptions{}); err != nil {
		t.Fatalf("Upload() error: %v", err)
	}

	if err := b.DeleteByPrefix(ctx, "tenant-acme"); err != nil {
		t.Fatalf("DeleteByPrefix() error: %v", err)
	}

	if _, _, err := b.Download(ctx, "tenant-acme/files/a.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Download() of a deleted prefix's file error = %v, want os.ErrNotExist", err)
	}
	if _, _, err := b.Download(ctx, "tenant-acme/files/b.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Download() of a deleted prefix's file error = %v, want os.ErrNotExist", err)
	}
	if _, size, err := b.Download(ctx, "tenant-other/c.txt"); err != nil || size == 0 {
		t.Errorf("Download() of an unrelated prefix's file: size=%d err=%v, want it to survive", size, err)
	}
}

func TestLocalBackendDeleteByPrefixMissingPrefixIsNotAnError(t *testing.T) {
	b := newTestLocalBackend(t)

	if err := b.DeleteByPrefix(context.Background(), "does-not-exist"); err != nil {
		t.Errorf("DeleteByPrefix() on a nonexistent prefix: error = %v, want nil", err)
	}
}

func TestLocalBackendSignedAndPublicURL(t *testing.T) {
	b := newTestLocalBackend(t)
	ctx := context.Background()

	signed, err := b.SignedURL(ctx, "some/key.txt", 0)
	if err != nil {
		t.Fatalf("SignedURL() error: %v", err)
	}
	if !strings.HasPrefix(signed, "file://") {
		t.Errorf("SignedURL() = %q, want a file:// URL", signed)
	}

	public, err := b.PublicURL(ctx, "some/key.txt")
	if err != nil {
		t.Fatalf("PublicURL() error: %v", err)
	}
	if !strings.HasPrefix(public, "file://") {
		t.Errorf("PublicURL() = %q, want a file:// URL", public)
	}
}
