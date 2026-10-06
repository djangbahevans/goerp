package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
)

func TestValidateUsesConfig(t *testing.T) {
	owner := func() *module.LoadedModule {
		return loadedModule(manifest.Manifest{
			Name:         "l10n_gh",
			ConfigSchema: []manifest.ConfigEntry{{Key: "vat_cert_number", Type: "string"}},
		})
	}

	tests := []struct {
		name       string
		ref        manifest.UsesConfigRef
		soft       []string
		owner      func() *module.LoadedModule
		wantErr    string
		wantLoaded bool
	}{
		{"declared with the expected type", manifest.UsesConfigRef{Key: "l10n_gh.vat_cert_number", Type: "string"}, nil, owner, "", true},
		{"owner does not declare the key", manifest.UsesConfigRef{Key: "l10n_gh.missing", Type: "string"}, nil, owner, `declares no config key "missing"`, false},
		{"incompatible type", manifest.UsesConfigRef{Key: "l10n_gh.vat_cert_number", Type: "integer"}, nil, owner, `expected as type "integer" but module "l10n_gh" declares it as "string"`, false},
		{"owner not loaded", manifest.UsesConfigRef{Key: "l10n_gh.vat_cert_number", Type: "string"}, nil, func() *module.LoadedModule { return nil }, "which is not loaded", false},
		{"soft owner not loaded", manifest.UsesConfigRef{Key: "l10n_gh.vat_cert_number", Type: "string"}, []string{"l10n_gh"}, func() *module.LoadedModule { return nil }, "", false},
		{"soft owner failed", manifest.UsesConfigRef{Key: "l10n_gh.vat_cert_number", Type: "string"}, []string{"l10n_gh"}, func() *module.LoadedModule {
			m := owner()
			m.Fail("broken")
			return m
		}, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := loadedModule(manifest.Manifest{
				Name: "billing", UsesConfig: []manifest.UsesConfigRef{tc.ref}, SoftDependsOn: tc.soft,
			})
			modules := map[string]*module.LoadedModule{"billing": reader}
			if o := tc.owner(); o != nil {
				modules["l10n_gh"] = o
			}

			ValidateUsesConfig(modules)

			if tc.wantErr != "" {
				if reader.Status != module.StatusFailed || !strings.Contains(reader.FailureReason, tc.wantErr) {
					t.Fatalf("status = %v, reason = %q, want a failure containing %q", reader.Status, reader.FailureReason, tc.wantErr)
				}
				return
			}
			if reader.Status == module.StatusFailed {
				t.Fatalf("reader failed: %s", reader.FailureReason)
			}
			got, ok := reader.UsesConfig[tc.ref.Key]
			if !ok || got.Loaded != tc.wantLoaded {
				t.Errorf("UsesConfig[%q] = %+v (present %v), want Loaded=%v", tc.ref.Key, got, ok, tc.wantLoaded)
			}
			if tc.wantLoaded && got.Entry.Key != "vat_cert_number" {
				t.Errorf("resolved entry = %+v, want the owner's", got.Entry)
			}
		})
	}
}

// A module this pass fails must not change the outcome for its readers,
// whichever order the map yields them in.
func TestValidateUsesConfig_OutcomeIndependentOfVisitOrder(t *testing.T) {
	for range 50 {
		owner := loadedModule(manifest.Manifest{
			Name:         "l10n_gh",
			ConfigSchema: []manifest.ConfigEntry{{Key: "vat_cert_number", Type: "string"}},
			UsesConfig:   []manifest.UsesConfigRef{{Key: "missing_mod.key", Type: "string"}},
		})
		reader := loadedModule(manifest.Manifest{
			Name:       "billing",
			UsesConfig: []manifest.UsesConfigRef{{Key: "l10n_gh.vat_cert_number", Type: "string"}},
		})

		ValidateUsesConfig(map[string]*module.LoadedModule{"l10n_gh": owner, "billing": reader})

		if owner.Status != module.StatusFailed {
			t.Fatal("owner with an unloaded hard dependency's key did not fail")
		}
		if reader.Status == module.StatusFailed {
			t.Fatalf("reader failed depending on visit order: %s", reader.FailureReason)
		}
	}
}
