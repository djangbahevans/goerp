package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageRejectsInvalidBackendTranslations(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"bad locale":   {"en.json": `{}`, "english.json": `{}`},
		"nested":       {"en.json": `{"a":{"b":"Name"}}`},
		"missing base": {"fr.json": `{"a":"Nom"}`},
		"null value":   {"en.json": `{"a":null}`},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeWasmFixture(t, dir, fixtureManifest)
			if err := os.MkdirAll(filepath.Join(dir, "translations"), 0o755); err != nil {
				t.Fatal(err)
			}

			for file, data := range files {
				if err := os.WriteFile(filepath.Join(dir, "translations", file), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			output := filepath.Join(dir, "out.erp")
			_, err := Package(t.Context(), dir, PackageOptions{SkipWasm: true, SkipFrontend: true, Output: output})
			if err == nil || !strings.Contains(err.Error(), "translations/") {
				t.Fatalf("error = %v", err)
			}

			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("failed build left archive: %v", err)
			}
		})
	}
}
