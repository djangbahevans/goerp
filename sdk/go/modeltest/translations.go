package modeltest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

func readBackendTranslations(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, l10n.BackendTranslationsDir, "*.json"))
	if err != nil {
		t.Fatalf("modeltest: find backend translations: %v", err)
	}
	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("modeltest: read backend translation: %v", err)
		}
		locale := strings.TrimSuffix(filepath.Base(path), ".json")
		files[locale] = data
	}
	return files
}

func setTranslationLookup(rt *wasm.Runtime, mod *module.LoadedModule) {
	// Harness-local files isolate fixtures that share the same module name.
	rt.SetTranslationLookup(func(locale, key string) (string, bool) {
		relative, ok := strings.CutPrefix(key, mod.Manifest.Name+".")
		if !ok {
			return "", false
		}
		value, found := mod.BackendTranslations[locale][relative]
		return value, found
	})
}
