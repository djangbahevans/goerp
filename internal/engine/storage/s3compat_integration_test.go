package storage_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/storage/storagetest"
)

func seaweedBackend(t *testing.T) storage.Backend {
	t.Helper()
	if os.Getenv("GOERP_TEST_S3_ENDPOINT") == "" {
		t.Skip("set GOERP_TEST_S3_ENDPOINT=http://localhost:8334 to run authenticated SeaweedFS tests")
	}

	b, err := storagetest.New(t)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func fetchObject(t *testing.T, objectURL string) (int, string, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, objectURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return resp.StatusCode, string(data), resp.Header.Get("Content-Type")
}

func TestSeaweedFSObjectsAndSignedURLs(t *testing.T) {
	b := seaweedBackend(t)
	ctx := t.Context()
	key := "attachments/tenant/a +#é.txt"
	content := "private content"

	id, err := b.Upload(ctx, key, io.LimitReader(strings.NewReader(content), int64(len(content))), storage.UploadOptions{ContentType: "text/plain"})
	if err != nil || id != key {
		t.Fatalf("upload = %q, %v", id, err)
	}

	r, size, err := b.Download(ctx, key)
	if err != nil {
		t.Fatal(err)
	}

	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || size != int64(len(content)) || string(got) != content {
		t.Fatalf("download = %q, %d, %v", got, size, err)
	}

	if exists, err := b.Exists(ctx, key); err != nil || !exists {
		t.Fatalf("exists = %v, %v", exists, err)
	}

	if _, err := b.PublicURL(ctx, key); !errors.Is(err, storage.ErrNotPublic) {
		t.Fatalf("private URL: %v", err)
	}

	signed, err := b.SignedURL(ctx, key, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(signed, "http://") {
		t.Fatalf("signed HTTP URL = %s", signed)
	}

	if status, body, ct := fetchObject(t, signed); status != http.StatusOK || body != content || ct != "text/plain" {
		t.Fatalf("signed GET = %d, %q, %q", status, body, ct)
	}

	unsigned, _, _ := strings.Cut(signed, "?")
	if status, _, _ := fetchObject(t, unsigned); status != http.StatusForbidden {
		t.Fatalf("anonymous private GET = %d", status)
	}

	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	if status, _, _ := fetchObject(t, signed); status != http.StatusForbidden {
		t.Fatalf("expired signed GET = %d", status)
	}

	if _, err := b.Upload(ctx, key, strings.NewReader("public"), storage.UploadOptions{ContentType: "text/custom", Public: true}); err != nil {
		t.Fatal(err)
	}

	public, err := b.PublicURL(ctx, key)
	if err != nil {
		t.Fatal(err)
	}

	if status, body, ct := fetchObject(t, public); status != http.StatusOK || body != "public" || ct != "text/custom" {
		t.Fatalf("public GET = %d, %q, %q", status, body, ct)
	}

	if _, err := b.Upload(ctx, key, strings.NewReader("private again"), storage.UploadOptions{}); err != nil {
		t.Fatal(err)
	}

	if _, err := b.PublicURL(ctx, key); !errors.Is(err, storage.ErrNotPublic) {
		t.Fatalf("replaced private URL: %v", err)
	}

	if status, _, _ := fetchObject(t, public); status != http.StatusForbidden {
		t.Fatalf("replaced private GET = %d", status)
	}

	if err := b.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}

	if err := b.Delete(ctx, key); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}

	if exists, err := b.Exists(ctx, key); err != nil || exists {
		t.Fatalf("missing exists = %v, %v", exists, err)
	}

	if r, _, err := b.Download(ctx, key); err == nil {
		_ = r.Close()
		t.Fatal("downloaded deleted object")
	}
}

func TestSeaweedFSMultipartStream(t *testing.T) {
	b := seaweedBackend(t)

	content := bytes.Repeat([]byte("streamed data"), 1024*1024)

	if _, err := b.Upload(t.Context(), "large", io.LimitReader(bytes.NewReader(content), int64(len(content))), storage.UploadOptions{}); err != nil {
		t.Fatal(err)
	}

	r, size, err := b.Download(t.Context(), "large")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = r.Close() }()

	got, err := io.ReadAll(r)
	if err != nil || size != int64(len(content)) || !bytes.Equal(got, content) {
		t.Fatalf("multipart download size = %d, error = %v", size, err)
	}
}

func TestSeaweedFSDeleteByPrefixPagination(t *testing.T) {
	b := seaweedBackend(t)
	ctx := t.Context()

	if _, err := b.Upload(ctx, "other/keep", strings.NewReader("keep"), storage.UploadOptions{}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	slots := make(chan struct{}, 12)

	for i := range 1005 {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()

			key := fmt.Sprintf("remove/%04d", i)

			if _, err := b.Upload(ctx, key, strings.NewReader("x"), storage.UploadOptions{}); err != nil {
				t.Errorf("upload %s: %v", key, err)
			}
		})
	}

	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}

	for range 2 {
		if err := b.DeleteByPrefix(ctx, "remove/"); err != nil {
			t.Fatal(err)
		}
	}

	for i := range 1005 {
		key := fmt.Sprintf("remove/%04d", i)
		if exists, err := b.Exists(ctx, key); err != nil || exists {
			t.Fatalf("deleted key %s exists = %v, %v", key, exists, err)
		}
	}

	if exists, err := b.Exists(ctx, "other/keep"); err != nil || !exists {
		t.Fatalf("unrelated object exists = %v, %v", exists, err)
	}
}
