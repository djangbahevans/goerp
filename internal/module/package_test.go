package module

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePackageFixture(t *testing.T, dir string) {
	t.Helper()

	writeWasmFixture(t, dir, fixtureManifest)
	writeMinimalFrontendFixture(t, dir, fixtureIndexTS)

	if err := os.MkdirAll(filepath.Join(dir, "translations"), 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "translations", "en.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write translations/en.json: %v", err)
	}
}

func zipEntryNames(t *testing.T, path string) []string {
	t.Helper()

	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer r.Close()

	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

func TestPackageAssemblesArchive(t *testing.T) {
	requireNpm(t)

	dir := t.TempDir()
	writePackageFixture(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := Package(ctx, dir, PackageOptions{})
	if err != nil {
		t.Fatalf("Package: %v", err)
	}

	if _, err := os.Stat(result.ArchivePath); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	wantPath := filepath.Join(dir, "build", "fixture-0.1.0.erp")
	if result.ArchivePath != wantPath {
		t.Errorf("ArchivePath = %q, want %q", result.ArchivePath, wantPath)
	}

	names := zipEntryNames(t, result.ArchivePath)
	hasPrefix := func(prefix string) bool {
		for _, n := range names {
			if n == prefix || strings.HasPrefix(n, prefix) {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"manifest.json", "module.wasm", "frontend/dist/bundle.", "translations/en.json"} {
		if !hasPrefix(want) {
			t.Errorf("archive entries %v missing expected member %q", names, want)
		}
	}

	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var decoded struct {
		Checksum string `json:"checksum"`
	}
	if err := json.Unmarshal(manifestData, &decoded); err != nil {
		t.Fatalf("unmarshal manifest.json: %v", err)
	}
	wasmData, err := os.ReadFile(filepath.Join(dir, "module.wasm"))
	if err != nil {
		t.Fatalf("read module.wasm: %v", err)
	}
	sum := sha256.Sum256(wasmData)
	wantChecksum := "sha256:" + hex.EncodeToString(sum[:])
	if decoded.Checksum != wantChecksum {
		t.Errorf("manifest checksum = %q, want %q", decoded.Checksum, wantChecksum)
	}

	archiveData, err := os.ReadFile(result.ArchivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	archiveSum := sha256.Sum256(archiveData)
	wantArchiveSHA := "sha256:" + hex.EncodeToString(archiveSum[:])
	if result.ArchiveSHA256 != wantArchiveSHA {
		t.Errorf("ArchiveSHA256 = %q, want %q", result.ArchiveSHA256, wantArchiveSHA)
	}

	sidecar, err := os.ReadFile(result.SidecarPath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	wantSidecar := archiveSum
	if !strings.HasPrefix(string(sidecar), hex.EncodeToString(wantSidecar[:])) {
		t.Errorf("sidecar = %q, want it to start with the archive's hex digest", sidecar)
	}
	if !strings.Contains(string(sidecar), filepath.Base(result.ArchivePath)) {
		t.Errorf("sidecar = %q, want it to name the archive file", sidecar)
	}
}

func TestPackageSkipWasmSkipFrontend(t *testing.T) {
	dir := t.TempDir()
	writeWasmFixture(t, dir, fixtureManifest)
	writeMinimalFrontendFixture(t, dir, fixtureIndexTS)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := Package(ctx, dir, PackageOptions{SkipWasm: true, SkipFrontend: true})
	if err != nil {
		t.Fatalf("Package: %v", err)
	}

	names := zipEntryNames(t, result.ArchivePath)
	for _, n := range names {
		if n == "module.wasm" || strings.HasPrefix(n, "frontend/") {
			t.Errorf("archive entries %v should not include wasm/frontend when both are skipped", names)
		}
	}
	if len(names) == 0 {
		t.Fatal("archive has no entries at all")
	}
}
