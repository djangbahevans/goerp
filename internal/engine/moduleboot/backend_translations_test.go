package moduleboot

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverAndParseBackendTranslations(t *testing.T) {
	members := map[string]string{"translations/en.json": `{"a":"A"}`, "translations/fr.json": `{"a":"À"}`, "frontend/translations/en.json": `{"a":"frontend"}`}
	data := erpWithMembers(t, members)
	src, _, err := ParsePackage(data)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"en": members["translations/en.json"], "fr": members["translations/fr.json"]}
	if !equalFiles(src.BackendTranslations, want) {
		t.Fatal(src.BackendTranslations)
	}

	if string(src.FrontendTranslations["en"]) != `{"a":"frontend"}` {
		t.Fatal(src.FrontendTranslations)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "widgets.erp"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	writeModuleDir(t, root, "gadgets", okModule, nil)
	dir := filepath.Join(root, "gadgets", "translations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for locale, text := range want {
		if err := os.WriteFile(filepath.Join(dir, locale+".json"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(sources) != 2 {
		t.Fatalf("sources = %v", sources)
	}

	for _, source := range sources {
		if !equalFiles(source.BackendTranslations, want) {
			t.Fatalf("%s: %v", source.Name, source.BackendTranslations)
		}
	}
}

func TestParsePackageRejectsDuplicateTranslationFiles(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	mustWriteZipEntry(t, writer, "manifest.json", manifestJSON(t, "widgets", okModule, nil))
	mustWriteZipEntry(t, writer, "module.wasm", okModule)
	mustWriteZipEntry(t, writer, "translations/en.json", []byte(`{"a":"valid"}`))
	mustWriteZipEntry(t, writer, "translations/en.json", []byte(`{"a":{"b":"invalid"}}`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParsePackage(archive.Bytes()); err == nil || !strings.Contains(err.Error(), "duplicate translation file") {
		t.Fatalf("error = %v", err)
	}
}
