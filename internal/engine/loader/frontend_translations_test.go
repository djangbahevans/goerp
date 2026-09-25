package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

func TestLoadModule_ValidFrontendTranslationsLoad(t *testing.T) {
	rt := newTestRuntime(t)
	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", okModule, []string{"db.read"}),
		WasmBytes:     okModule,
		FrontendTranslations: map[string][]byte{
			"en":    []byte(`{"actions.create":"New widget"}`),
			"pt-BR": []byte(`{"actions.create":"Novo widget"}`),
		},
	}

	m := LoadModule(t.Context(), rt, testPoolCfg(), src)

	if m.Status != module.StatusSyncing {
		t.Fatalf("Status = %v, want StatusSyncing; FailureReason = %q", m.Status, m.FailureReason)
	}
}

func TestLoadModule_InvalidFrontendTranslationFails(t *testing.T) {
	for name, tc := range map[string]struct {
		locale, data, want string
	}{
		"bad locale": {"english", `{}`, "frontend/translations/english.json"},
		"nested":     {"fr", `{"fields":{"name":"Nom"}}`, "frontend/translations/fr.json"},
	} {
		rt := newTestRuntime(t)
		src := Source{
			Name:                 "widgets",
			ManifestBytes:        manifestJSON(t, "widgets", okModule, []string{"db.read"}),
			WasmBytes:            okModule,
			FrontendTranslations: map[string][]byte{tc.locale: []byte(tc.data)},
		}

		m := LoadModule(t.Context(), rt, testPoolCfg(), src)

		if m.Status != module.StatusFailed {
			t.Errorf("%s: Status = %v, want StatusFailed", name, m.Status)
			continue
		}
		if !strings.Contains(m.FailureReason, tc.want) {
			t.Errorf("%s: FailureReason = %q, want it to name %s", name, m.FailureReason, tc.want)
		}
	}
}
