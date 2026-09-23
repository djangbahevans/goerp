package module

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/storage"
)

func TestBundleFilename_NoFrontendDeclared(t *testing.T) {
	got, err := BundleFilename(&manifest.Manifest{})
	if err != nil {
		t.Fatalf("BundleFilename() error: %v", err)
	}
	if got != "" {
		t.Errorf("BundleFilename() = %q, want \"\"", got)
	}
}

func TestBundleFilename_BundleFalse(t *testing.T) {
	mf := &manifest.Manifest{Frontend: &manifest.FrontendConfig{Bundle: new(false)}}
	got, err := BundleFilename(mf)
	if err != nil {
		t.Fatalf("BundleFilename() error: %v", err)
	}
	if got != "" {
		t.Errorf("BundleFilename() = %q, want \"\"", got)
	}
}

func TestBundleFilename_DerivesFromBundleSHA256(t *testing.T) {
	mf := &manifest.Manifest{Frontend: &manifest.FrontendConfig{
		Bundle:       new(true),
		BundleSHA256: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd",
	}}
	got, err := BundleFilename(mf)
	if err != nil {
		t.Fatalf("BundleFilename() error: %v", err)
	}
	if want := "bundle.0123456789ab.js"; got != want {
		t.Errorf("BundleFilename() = %q, want %q", got, want)
	}
}

func TestBundleFilename_MalformedDigestErrors(t *testing.T) {
	mf := &manifest.Manifest{Frontend: &manifest.FrontendConfig{Bundle: new(true), BundleSHA256: "not-a-digest"}}
	if _, err := BundleFilename(mf); err == nil {
		t.Fatal("BundleFilename() error = nil, want an error for a malformed digest")
	}
}

func TestBundleStorageKey(t *testing.T) {
	if got, want := BundleStorageKey("widgets", "bundle.abc123abc123.js"), "widgets/bundle.abc123abc123.js"; got != want {
		t.Errorf("BundleStorageKey() = %q, want %q", got, want)
	}
}

// fakeBackend is a minimal in-memory storage.Backend, enough to exercise
// PublishBundle's Upload call without a real local-disk backend (which
// needs GOERP_STORAGE_LOCAL_DIR set).
type fakeBackend struct {
	uploaded map[string][]byte
}

func (b *fakeBackend) Upload(_ context.Context, key string, r io.Reader, _ storage.UploadOptions) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if b.uploaded == nil {
		b.uploaded = map[string][]byte{}
	}
	b.uploaded[key] = data
	return key, nil
}

func (b *fakeBackend) Download(_ context.Context, key string) (io.ReadCloser, int64, error) {
	data, ok := b.uploaded[key]
	if !ok {
		return nil, 0, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (b *fakeBackend) Delete(context.Context, string) error         { return nil }
func (b *fakeBackend) DeleteByPrefix(context.Context, string) error { return nil }
func (b *fakeBackend) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (b *fakeBackend) PublicURL(context.Context, string) (string, error) { return "", nil }
func (b *fakeBackend) Exists(_ context.Context, key string) (bool, error) {
	_, ok := b.uploaded[key]
	return ok, nil
}

func TestPublishBundle_NoFrontendDeclared_NoOp(t *testing.T) {
	backend := &fakeBackend{}
	if err := PublishBundle(context.Background(), backend, "widgets", &manifest.Manifest{}, nil); err != nil {
		t.Fatalf("PublishBundle() error: %v", err)
	}
	if len(backend.uploaded) != 0 {
		t.Errorf("uploaded = %v, want nothing uploaded", backend.uploaded)
	}
}

func TestPublishBundle_NilBackend_ReturnsErrNoStorageBackend(t *testing.T) {
	mf := &manifest.Manifest{Frontend: &manifest.FrontendConfig{
		Bundle:       new(true),
		BundleSHA256: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd",
	}}
	err := PublishBundle(context.Background(), nil, "widgets", mf, []byte("bytes"))
	if !errors.Is(err, ErrNoStorageBackend) {
		t.Fatalf("PublishBundle() error = %v, want ErrNoStorageBackend", err)
	}
}

func TestPublishBundle_UploadsUnderModuleAndFilenameKey(t *testing.T) {
	mf := &manifest.Manifest{Frontend: &manifest.FrontendConfig{
		Bundle:       new(true),
		BundleSHA256: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd",
	}}
	backend := &fakeBackend{}
	if err := PublishBundle(context.Background(), backend, "widgets", mf, []byte("bytes")); err != nil {
		t.Fatalf("PublishBundle() error: %v", err)
	}

	want := "widgets/bundle.0123456789ab.js"
	if data, ok := backend.uploaded[want]; !ok || string(data) != "bytes" {
		t.Errorf("uploaded[%q] = %q, ok=%v, want \"bytes\", ok=true", want, data, ok)
	}
}
