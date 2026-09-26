package module

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"slices"
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

func zipMembers(t *testing.T, path string) map[string][]byte {
	t.Helper()

	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer r.Close()

	members := make(map[string][]byte, len(r.File))
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		members[f.Name] = data
	}
	return members
}

func sha256Ref(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestPackageAssemblesArchive(t *testing.T) {
	requireNpm(t)

	dir := t.TempDir()
	writePackageFixture(t, dir)

	// 5m, not 3m — Package's own npm install shares the same cold-cache
	// exposure as build_test.go's frontend-build tests (goerp#585); by
	// the time this test runs, an earlier npm-touching test in the
	// package has usually already warmed the local cache, but this
	// budget still covers the case where this is the first one to run.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sourceManifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}

	result, err := Package(ctx, dir, PackageOptions{})
	if err != nil {
		t.Fatalf("Package: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	if string(after) != string(sourceManifest) {
		t.Errorf("Package modified the source manifest.json:\n%s", after)
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

	// The packaged manifest's hashes must match the packaged members,
	// the same checks the engine's loader applies (verifyChecksum,
	// verifyBundle).
	members := zipMembers(t, result.ArchivePath)
	var packaged struct {
		Checksum string `json:"checksum"`
		Frontend struct {
			BundleSHA256 string `json:"bundle_sha256"`
		} `json:"frontend"`
	}
	if err := json.Unmarshal(members["manifest.json"], &packaged); err != nil {
		t.Fatalf("unmarshal packaged manifest.json: %v", err)
	}
	if want := sha256Ref(members["module.wasm"]); packaged.Checksum != want {
		t.Errorf("packaged checksum = %q, want %q", packaged.Checksum, want)
	}
	var bundle []byte
	for name, data := range members {
		if strings.HasPrefix(name, "frontend/dist/bundle.") {
			bundle = data
		}
	}
	if want := sha256Ref(bundle); packaged.Frontend.BundleSHA256 != want {
		t.Errorf("packaged frontend.bundle_sha256 = %q, want %q", packaged.Frontend.BundleSHA256, want)
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

func writeFrontendTranslations(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	tdir := filepath.Join(dir, "frontend", "translations")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatalf("mkdir frontend/translations: %v", err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(tdir, name), []byte(data), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestPackageIncludesFrontendTranslations(t *testing.T) {
	dir := t.TempDir()
	writeWasmFixture(t, dir, fixtureManifest)
	writeFrontendTranslations(t, dir, map[string]string{
		"en.json": `{"actions.create":"New Contact"}`,
		"fr.json": `{"actions.create":"Nouveau contact"}`,
	})
	if err := os.MkdirAll(filepath.Join(dir, "translations"), 0o755); err != nil {
		t.Fatalf("mkdir translations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "translations", "en.json"), []byte(`{"errors.not_found":"Not found"}`), 0o644); err != nil {
		t.Fatalf("write translations/en.json: %v", err)
	}

	result, err := Package(t.Context(), dir, PackageOptions{SkipWasm: true, SkipFrontend: true})
	if err != nil {
		t.Fatalf("Package: %v", err)
	}

	names := zipEntryNames(t, result.ArchivePath)
	for _, want := range []string{"translations/en.json", "frontend/translations/en.json", "frontend/translations/fr.json"} {
		if !slices.Contains(names, want) {
			t.Errorf("archive entries %v are missing %s", names, want)
		}
	}
}

func TestPackageRejectsAnInvalidFrontendTranslation(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"bad locale name": {"english.json": `{}`},
		"nested object":   {"en.json": `{"fields":{"name":"Name"}}`},
	} {
		dir := t.TempDir()
		writeWasmFixture(t, dir, fixtureManifest)
		writeFrontendTranslations(t, dir, files)

		output := filepath.Join(dir, "out.erp")
		_, err := Package(t.Context(), dir, PackageOptions{SkipWasm: true, SkipFrontend: true, Output: output})
		if err == nil {
			t.Errorf("%s: Package succeeded, want an error", name)
			continue
		}
		if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
			t.Errorf("%s: a failed build left out.erp behind (stat error %v)", name, statErr)
		}
	}
}
