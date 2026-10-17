package storage

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalBackendFailedUploadDoesNotPublishPartialObjects(t *testing.T) {
	b := newTestLocalBackend(t)
	failure := errors.New("source read failed")
	for _, existing := range []bool{false, true} {
		if existing {
			if _, err := b.Upload(t.Context(), "archive/data.parquet.gz", strings.NewReader("complete"), UploadOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		reader := io.MultiReader(strings.NewReader("partial"), failingLocalReader{err: failure})
		if _, err := b.Upload(t.Context(), "archive/data.parquet.gz", reader, UploadOptions{}); !errors.Is(err, failure) {
			t.Fatalf("Upload error=%v, want source failure", err)
		}
		exists, err := b.Exists(t.Context(), "archive/data.parquet.gz")
		if err != nil || exists != existing {
			t.Fatalf("Exists=%v, err=%v, want %v", exists, err, existing)
		}
		if existing {
			data, err := os.ReadFile(filepath.Join(b.dir, "archive/data.parquet.gz"))
			if err != nil || string(data) != "complete" {
				t.Fatalf("original object changed: %q, err=%v", data, err)
			}
		}
		files, err := os.ReadDir(filepath.Join(b.dir, "archive"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasPrefix(file.Name(), ".upload-") {
				t.Errorf("failed upload left temporary file %s", file.Name())
			}
		}
	}
}

type failingLocalReader struct{ err error }

func (r failingLocalReader) Read([]byte) (int, error) { return 0, r.err }

func newTestLocalBackend(t *testing.T) *LocalBackend {
	t.Helper()

	backend, newErr := NewLocal(t.TempDir())
	if newErr != nil {
		t.Fatalf("NewLocal(dir) error: %v", newErr)
	}
	return backend
}

func TestNewLocalIsolatesBackendDirectories(t *testing.T) {
	first, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := first.Upload(t.Context(), "same-key.txt", strings.NewReader("first"), UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	exists, err := second.Exists(t.Context(), "same-key.txt")
	if err != nil || exists {
		t.Fatalf("second backend contains first backend's object: exists = %t, error = %v", exists, err)
	}
}

func TestLocalBackendUploadDownload(t *testing.T) {
	b := newTestLocalBackend(t)
	ctx := t.Context()
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

	_, _, err := b.Download(t.Context(), "does/not/exist.txt")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Download() of a missing key error = %v, want a wrapped os.ErrNotExist", err)
	}
}

func TestLocalBackendDelete(t *testing.T) {
	b := newTestLocalBackend(t)
	ctx := t.Context()

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
	ctx := t.Context()

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

	if err := b.DeleteByPrefix(t.Context(), "does-not-exist"); err != nil {
		t.Errorf("DeleteByPrefix() on a nonexistent prefix: error = %v, want nil", err)
	}
}

func TestLocalBackendSignedAndPublicURL(t *testing.T) {
	b := newTestLocalBackend(t)
	ctx := t.Context()
	if _, err := b.Upload(ctx, "some/key.txt", strings.NewReader("file"), UploadOptions{}); err != nil {
		t.Fatal(err)
	}

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
