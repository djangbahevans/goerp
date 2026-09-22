package module

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func writeManifestFile(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	return path
}

func TestReadManifestJSON_ValidManifestDecodes(t *testing.T) {
	path := writeManifestFile(t, fixtureManifest)

	decoded, err := readManifestJSON(path)
	if err != nil {
		t.Fatalf("readManifestJSON: %v", err)
	}
	if got, _ := decoded["name"].(string); got != "fixture" {
		t.Errorf("decoded[name] = %q, want %q", got, "fixture")
	}
}

func TestReadManifestJSON_DuplicateKeyRejected(t *testing.T) {
	path := writeManifestFile(t, `{"name":"fixture","name":"fixture"}`)

	_, err := readManifestJSON(path)
	if !errors.Is(err, manifest.ErrInvalidJSON) {
		t.Fatalf("readManifestJSON error = %v, want %v", err, manifest.ErrInvalidJSON)
	}
}

func TestReadManifestJSON_InvalidUTF8Rejected(t *testing.T) {
	path := writeManifestFile(t, "")
	if err := os.WriteFile(path, []byte(`{"name":"`+"\xff\xfe"+`"}`), 0o644); err != nil {
		t.Fatalf("write invalid-utf8 manifest: %v", err)
	}

	_, err := readManifestJSON(path)
	if !errors.Is(err, manifest.ErrInvalidUtf8) {
		t.Fatalf("readManifestJSON error = %v, want %v", err, manifest.ErrInvalidUtf8)
	}
}
