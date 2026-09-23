package module

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/storage"
)

// bundleFilenameHashLen is how many hex characters of frontend.bundle_sha256
// name the served bundle file — matches internal/module/build.go's own
// fullHex[:12] truncation when it renames Vite's build output to
// bundle.<hash>.js, so a module's declared bundle_sha256 always resolves to
// the exact filename that shipped in its .erp archive.
const bundleFilenameHashLen = 12

// BundleFilename derives the "bundle.<hash>.js" filename GET
// /modules/{module}/frontend/{file} serves from mf.Frontend.BundleSHA256 —
// "" if mf declares no frontend bundle. An error here means
// frontend.bundle_sha256 isn't the "sha256:<hex>" shape manifest validation
// (validate:"sha256_checksum") already requires, so it should only ever be
// reachable for a manifest that bypassed that validation somehow.
func BundleFilename(mf *manifest.Manifest) (string, error) {
	if mf.Frontend == nil || mf.Frontend.Bundle == nil || !*mf.Frontend.Bundle {
		return "", nil
	}

	hexPart, ok := strings.CutPrefix(mf.Frontend.BundleSHA256, "sha256:")
	if !ok || len(hexPart) < bundleFilenameHashLen {
		return "", fmt.Errorf("frontend.bundle_sha256 %q is not a valid sha256:<hex> digest", mf.Frontend.BundleSHA256)
	}

	return "bundle." + hexPart[:bundleFilenameHashLen] + ".js", nil
}

// BundleStorageKey is the storage.Backend key a module's bundle is
// published under and served from — derived from moduleName and filename
// alone, so GET /modules/{module}/frontend/{file} can reconstruct it purely
// from the request path. A previous version's key stays servable this way,
// independent of whichever version is currently loaded, since filename
// itself carries that version's own content hash (BundleFilename).
func BundleStorageKey(moduleName, filename string) string {
	return moduleName + "/" + filename
}

// ErrNoStorageBackend is PublishBundle's rejection when mf declares a
// frontend bundle but backend is nil — object storage is a warn-only
// Engine startup dependency (engine-internals.md §2), so this is a
// real, expected condition every caller must handle (typically by
// logging and continuing), not something PublishBundle can silently
// swallow the way it does a manifest declaring no bundle at all.
var ErrNoStorageBackend = errors.New("no object storage backend configured")

// PublishBundle uploads bundleBytes to backend under
// BundleStorageKey(moduleName, ...) when mf declares a frontend bundle —
// shared by every load path that can make a module's bundle newly
// servable (moduleboot.LoadCascading at startup, moduleinstall.Worker,
// modulereload.Leader), so the key convention and upload options can't
// drift between them. A manifest declaring no bundle is a silent no-op
// (nil, nil); nothing to publish.
func PublishBundle(ctx context.Context, backend storage.Backend, moduleName string, mf *manifest.Manifest, bundleBytes []byte) error {
	filename, err := BundleFilename(mf)
	if err != nil || filename == "" {
		return err
	}
	if backend == nil {
		return ErrNoStorageBackend
	}

	key := BundleStorageKey(moduleName, filename)
	_, err = backend.Upload(ctx, key, bytes.NewReader(bundleBytes), storage.UploadOptions{ContentType: "text/javascript"})
	return err
}
