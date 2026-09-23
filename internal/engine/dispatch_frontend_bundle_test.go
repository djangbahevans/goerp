package engine

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/storage"
)

// fakeBundleBackend is a minimal in-memory storage.Backend, enough to
// exercise dispatchFrontendBundleRoute's Exists/Download calls without a
// real local-disk backend.
type fakeBundleBackend struct {
	files map[string][]byte
}

func (b *fakeBundleBackend) Upload(_ context.Context, key string, r io.Reader, _ storage.UploadOptions) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if b.files == nil {
		b.files = map[string][]byte{}
	}
	b.files[key] = data
	return key, nil
}

func (b *fakeBundleBackend) Download(_ context.Context, key string) (io.ReadCloser, int64, error) {
	data, ok := b.files[key]
	if !ok {
		return nil, 0, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (b *fakeBundleBackend) Delete(context.Context, string) error         { return nil }
func (b *fakeBundleBackend) DeleteByPrefix(context.Context, string) error { return nil }
func (b *fakeBundleBackend) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (b *fakeBundleBackend) PublicURL(context.Context, string) (string, error) { return "", nil }
func (b *fakeBundleBackend) Exists(_ context.Context, key string) (bool, error) {
	_, ok := b.files[key]
	return ok, nil
}

func bundleRequest(moduleName, file string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/modules/"+moduleName+"/frontend/"+file, nil)
	ctx := route.WithParams(r.Context(), map[string]string{"module": moduleName, "file": file})
	return r.WithContext(ctx)
}

func TestDispatchFrontendBundleRoute_ServesBundleBytes(t *testing.T) {
	backend := &fakeBundleBackend{}
	if _, err := backend.Upload(context.Background(), "widgets/bundle.0123456789ab.js", bytes.NewReader([]byte("export default 1;")), storage.UploadOptions{}); err != nil {
		t.Fatalf("seed Upload() error: %v", err)
	}
	e := &Engine{storageBackend: backend}

	w := httptest.NewRecorder()
	e.dispatchFrontendBundleRoute(w, bundleRequest("widgets", "bundle.0123456789ab.js"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "export default 1;" {
		t.Errorf("body = %q, want %q", got, "export default 1;")
	}
	if got := w.Header().Get("Content-Type"); got != "text/javascript" {
		t.Errorf("Content-Type = %q, want %q", got, "text/javascript")
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want the immutable directive", got)
	}
}

func TestDispatchFrontendBundleRoute_UnknownModuleOrFile404s(t *testing.T) {
	e := &Engine{storageBackend: &fakeBundleBackend{}}

	w := httptest.NewRecorder()
	e.dispatchFrontendBundleRoute(w, bundleRequest("widgets", "bundle.0123456789ab.js"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchFrontendBundleRoute_MalformedFilename404sWithoutTouchingStorage(t *testing.T) {
	backend := &fakeBundleBackend{}
	e := &Engine{storageBackend: backend}

	for _, file := range []string{"bundle.js", "bundle.xyz.js", "../secret", "bundle.0123456789ab.ts", "bundle.0123456789abcd.js"} {
		w := httptest.NewRecorder()
		e.dispatchFrontendBundleRoute(w, bundleRequest("widgets", file))
		if w.Code != http.StatusNotFound {
			t.Errorf("file %q: status = %d, want 404", file, w.Code)
		}
	}
}

func TestDispatchFrontendBundleRoute_NoStorageBackendReturns503(t *testing.T) {
	e := &Engine{}

	w := httptest.NewRecorder()
	e.dispatchFrontendBundleRoute(w, bundleRequest("widgets", "bundle.0123456789ab.js"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "storage_unavailable" {
		t.Errorf("error.code = %q, want %q", body.Error.Code, "storage_unavailable")
	}
}

func TestDispatchFrontendBundleRoute_PreviousVersionStaysServable(t *testing.T) {
	backend := &fakeBundleBackend{}
	ctx := context.Background()
	if _, err := backend.Upload(ctx, "widgets/bundle.aaaaaaaaaaaa.js", bytes.NewReader([]byte("old")), storage.UploadOptions{}); err != nil {
		t.Fatalf("seed old version Upload() error: %v", err)
	}
	if _, err := backend.Upload(ctx, "widgets/bundle.bbbbbbbbbbbb.js", bytes.NewReader([]byte("new")), storage.UploadOptions{}); err != nil {
		t.Fatalf("seed new version Upload() error: %v", err)
	}
	e := &Engine{storageBackend: backend}

	for file, want := range map[string]string{"bundle.aaaaaaaaaaaa.js": "old", "bundle.bbbbbbbbbbbb.js": "new"} {
		w := httptest.NewRecorder()
		e.dispatchFrontendBundleRoute(w, bundleRequest("widgets", file))
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("file %q: status = %d, body = %q, want 200 %q", file, w.Code, w.Body.String(), want)
		}
	}
}
