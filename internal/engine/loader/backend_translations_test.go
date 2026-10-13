package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

func TestLoadModule_BackendTranslations(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string][]byte
		want  string
	}{
		"none":         {},
		"valid":        {files: map[string][]byte{"en": []byte(`{"a.b":"Name"}`), "fr": []byte(`{"a.b":"Nom"}`)}},
		"missing base": {files: map[string][]byte{"fr": []byte(`{}`)}, want: "translations/en.json"},
		"bad locale":   {files: map[string][]byte{"en": []byte(`{}`), "english": []byte(`{}`)}, want: "translations/english.json"},
		"nested":       {files: map[string][]byte{"en": []byte(`{"a":{"b":"Name"}}`)}, want: "flat JSON object of strings"},
	} {
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t)
			loaded := LoadModule(t.Context(), rt, testPoolCfg(), Source{
				Name:                "widgets",
				ManifestBytes:       manifestJSON(t, "widgets", okModule, []string{"db.read"}),
				WasmBytes:           okModule,
				BackendTranslations: tc.files,
			})
			if tc.want != "" {
				if loaded.Status != module.StatusFailed || !strings.Contains(loaded.FailureReason, tc.want) {
					t.Fatalf("status = %v, reason = %q, want %q", loaded.Status, loaded.FailureReason, tc.want)
				}
				return
			}

			if loaded.Status != module.StatusSyncing {
				t.Fatal(loaded.FailureReason)
			}

			if name == "valid" && loaded.BackendTranslations["fr"]["a.b"] != "Nom" {
				t.Fatal(loaded.BackendTranslations)
			}
		})
	}
}
