package moduleboot

import (
	"archive/zip"
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func erpWithMembers(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	mustWriteZipEntry(t, w, "manifest.json", manifestJSON(t, "widgets", okModule, nil))
	mustWriteZipEntry(t, w, "module.wasm", okModule)
	for name, data := range members {
		mustWriteZipEntry(t, w, name, []byte(data))
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

func equalFiles(got map[string][]byte, want map[string]string) bool {
	return maps.EqualFunc(got, want, func(g []byte, w string) bool { return string(g) == w })
}

func TestParsePackage_ReadsFrontendTranslationsByLocale(t *testing.T) {
	data := erpWithMembers(t, map[string]string{
		"frontend/translations/en.json":      `{"a":"A"}`,
		"frontend/translations/fr.json":      `{"a":"À"}`,
		"frontend/translations/README.md":    "not a translation",
		"frontend/translations/old/de.json":  `{}`,
		"translations/en.json":               `{"backend":"only"}`,
		"frontend/dist/translations/pt.json": `{}`,
	})
	src, _, err := ParsePackage(data)
	if err != nil {
		t.Fatalf("ParsePackage() error = %v", err)
	}
	if want := map[string]string{"en": `{"a":"A"}`, "fr": `{"a":"À"}`}; !equalFiles(src.FrontendTranslations, want) {
		t.Errorf("FrontendTranslations = %q, want %q", src.FrontendTranslations, want)
	}
}

func TestParsePackage_NoFrontendTranslationsIsNil(t *testing.T) {
	src, _, err := ParsePackage(buildErpBytes(t, manifestJSON(t, "widgets", okModule, nil), okModule))
	if err != nil {
		t.Fatalf("ParsePackage() error = %v", err)
	}
	if src.FrontendTranslations != nil {
		t.Errorf("FrontendTranslations = %q, want nil", src.FrontendTranslations)
	}
}

func TestDiscover_ReadsFrontendTranslationsFromErpAndDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "widgets.erp"), erpWithMembers(t, map[string]string{"frontend/translations/en.json": `{"a":"A"}`}), 0o644); err != nil {
		t.Fatalf("write widgets.erp: %v", err)
	}
	writeModuleDir(t, root, "gadgets", okModule, nil)
	tdir := filepath.Join(root, "gadgets", "frontend", "translations")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tdir, "ar.json"), []byte(`{"a":"أ"}`), 0o644); err != nil {
		t.Fatalf("write ar.json: %v", err)
	}

	sources, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	got := map[string]map[string][]byte{}
	for _, src := range sources {
		got[src.Name] = src.FrontendTranslations
	}
	if !equalFiles(got["widgets"], map[string]string{"en": `{"a":"A"}`}) {
		t.Errorf("widgets.erp FrontendTranslations = %q", got["widgets"])
	}
	if !equalFiles(got["gadgets"], map[string]string{"ar": `{"a":"أ"}`}) {
		t.Errorf("gadgets/ FrontendTranslations = %q", got["gadgets"])
	}
}
