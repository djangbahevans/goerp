package module

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"

	"github.com/djangbahevans/goerp/internal/engine/storage"
)

// A module version's frontend translations are stored content-addressed,
// {module}/translations/{version}/{digest}/{locale}.json, next to a
// {module}/translations/{version}/current object naming the live digest.
// Republishing the same version (a development hot reload, a restart)
// uploads its set under its own digest and only then moves current, so a
// reload that fails after uploading leaves the live set untouched, a
// locale the new set drops stops being served, and an unchanged set
// rewrites identical objects without a moment of absence.

// maxPointerSize bounds how much of a current object is read: it only
// ever holds a hex digest.
const maxPointerSize = 256

func translationsPrefix(moduleName, version string) string {
	return moduleName + "/translations/" + version + "/"
}

// FrontendTranslationStorageKey is where one locale of the translation set
// with digest is stored.
func FrontendTranslationStorageKey(moduleName, version, digest, locale string) string {
	return translationsPrefix(moduleName, version) + digest + "/" + locale + ".json"
}

func frontendTranslationsPointerKey(moduleName, version string) string {
	return translationsPrefix(moduleName, version) + "current"
}

// frontendTranslationsDigest identifies a translation set by its locales
// and their contents.
func frontendTranslationsDigest(files map[string][]byte) string {
	h := sha256.New()
	for _, locale := range slices.Sorted(maps.Keys(files)) {
		h.Write([]byte(locale + "\x00" + strconv.Itoa(len(files[locale])) + "\x00"))
		h.Write(files[locale])
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// UploadFrontendTranslations stores files (locale to bytes) under their
// set's digest and returns it, without making them live: that is
// ActivateFrontendTranslations. No files uploads nothing and returns "".
func UploadFrontendTranslations(ctx context.Context, backend storage.Backend, moduleName, version string, files map[string][]byte) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	if backend == nil {
		return "", ErrNoStorageBackend
	}
	digest := frontendTranslationsDigest(files)
	for _, locale := range slices.Sorted(maps.Keys(files)) {
		key := FrontendTranslationStorageKey(moduleName, version, digest, locale)
		opts := storage.UploadOptions{ContentType: "application/json"}
		if _, err := backend.Upload(ctx, key, bytes.NewReader(files[locale]), opts); err != nil {
			return "", fmt.Errorf("upload %s translations: %w", locale, err)
		}
	}
	return digest, nil
}

// ActivateFrontendTranslations makes the set with digest the one served
// for moduleName@version. An empty digest (a version with no frontend
// translations) clears whatever set was live.
func ActivateFrontendTranslations(ctx context.Context, backend storage.Backend, moduleName, version, digest string) error {
	if backend == nil {
		if digest == "" {
			return nil
		}
		return ErrNoStorageBackend
	}
	key := frontendTranslationsPointerKey(moduleName, version)
	if digest == "" {
		exists, err := backend.Exists(ctx, key)
		if err != nil || !exists {
			return err
		}
		if err := backend.Delete(ctx, key); err != nil {
			return fmt.Errorf("clear live translations: %w", err)
		}
		return nil
	}
	if _, err := backend.Upload(ctx, key, bytes.NewReader([]byte(digest)), storage.UploadOptions{ContentType: "text/plain"}); err != nil {
		return fmt.Errorf("set live translations: %w", err)
	}
	return nil
}

// PublishFrontendTranslations uploads and activates files in one step, for
// the load paths where the module is new to this engine (startup, install)
// and there is no live set a failure could leave half-replaced.
func PublishFrontendTranslations(ctx context.Context, backend storage.Backend, moduleName, version string, files map[string][]byte) error {
	digest, err := UploadFrontendTranslations(ctx, backend, moduleName, version, files)
	if err != nil {
		return err
	}
	return ActivateFrontendTranslations(ctx, backend, moduleName, version, digest)
}

// LiveFrontendTranslationKey is the storage key of locale in
// moduleName@version's live set, or "" when the version has no live set.
// Whether that locale's file exists is the caller's to check.
func LiveFrontendTranslationKey(ctx context.Context, backend storage.Backend, moduleName, version, locale string) (string, error) {
	pointer := frontendTranslationsPointerKey(moduleName, version)
	exists, err := backend.Exists(ctx, pointer)
	if err != nil || !exists {
		return "", err
	}
	rc, _, err := backend.Download(ctx, pointer)
	if err != nil {
		return "", fmt.Errorf("read live translations: %w", err)
	}
	defer func() { _ = rc.Close() }()
	digest, err := io.ReadAll(io.LimitReader(rc, maxPointerSize))
	if err != nil {
		return "", fmt.Errorf("read live translations: %w", err)
	}
	if len(digest) == 0 {
		return "", nil
	}
	return FrontendTranslationStorageKey(moduleName, version, string(digest), locale), nil
}
