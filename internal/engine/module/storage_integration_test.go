package module

import (
	"io"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/storage/storagetest"
)

func TestPublishBundleStorage(t *testing.T) {
	backend, err := storagetest.New(t)
	if err != nil {
		t.Fatal(err)
	}

	mf := &manifest.Manifest{Frontend: &manifest.FrontendConfig{
		Bundle:       new(true),
		BundleSHA256: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd",
	}}

	if err := PublishBundle(t.Context(), backend, "widgets", mf, []byte("export default {}")); err != nil {
		t.Fatal(err)
	}

	r, size, err := backend.Download(t.Context(), "widgets/bundle.0123456789ab.js")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = r.Close() }()

	data, err := io.ReadAll(r)
	if err != nil || string(data) != "export default {}" || size != int64(len(data)) {
		t.Fatalf("stored bundle = %q, size %d, error %v", data, size, err)
	}
}
