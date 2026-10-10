package wasm

import (
	"context"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/vmihailenco/msgpack/v5"
)

type readOnlyConfigStore struct {
	values   map[string]map[string]string
	profiles map[string]*tenant.Profile
}

func (s *readOnlyConfigStore) Get(_ context.Context, tenantID, key string) (string, bool, bool, error) {
	v, found := s.values[tenantID][key]
	return v, false, found, nil
}

func (*readOnlyConfigStore) Invalidate(_, _ string) {}

func (s *readOnlyConfigStore) GetProfile(_ context.Context, tenantID string) (*tenant.Profile, error) {
	return s.profiles[tenantID], nil
}

func TestConfigCallerFixture_ReadOnlyTenantContextAndSoftDependency(t *testing.T) {
	rt := newHostcallTestRuntime(t, nil, 10)
	store := &readOnlyConfigStore{
		values: map[string]map[string]string{
			"tenant-1": {"l10n_gh.vat_cert_number": "VAT-1", "soft_mod.flag": "true"},
			"tenant-2": {"l10n_gh.vat_cert_number": "VAT-2", "soft_mod.flag": "true"},
		},
		profiles: map[string]*tenant.Profile{
			"tenant-1": {
				Name:    "Company 1",
				Address: new("Address 1"),
				TaxID:   new("TIN-1"),
				LogoURL: new("https://example.test/1.png"),
			},
			"tenant-2": {
				Name:    "Company 2",
				Address: new("Address 2"),
				TaxID:   new("TIN-2"),
			},
		},
	}
	rt.SetTenantConfig(store, nil)
	rt.SetCompanyProfileStore(store)

	compiled, err := rt.wazero.CompileModule(t.Context(), compileConfigCallerFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := newModuleInstance(t.Context(), "read-only-config", compiled, rt)
	if err != nil {
		t.Fatal(err)
	}
	rt.RegisterInstance(inst)
	t.Cleanup(func() { rt.UnregisterInstance(inst) })

	read := func(tenantID string, softLoaded bool) map[string]any {
		t.Helper()
		inst.SetModuleContext(NewModuleContext("req", "reports", "user", "", nil, nil, tenantID, "", "trace", 0, nil, ModuleSnapshot{
			UsesConfig: map[string]manifest.UsesConfigEntry{
				"l10n_gh.vat_cert_number": {Entry: manifest.ConfigEntry{Key: "vat_cert_number", Type: "string"}, Loaded: true},
				"soft_mod.flag":           {Entry: manifest.ConfigEntry{Key: "flag", Type: "boolean"}, Loaded: softLoaded},
			},
		}))

		results, err := inst.module.ExportedFunction("run_read_only").Call(t.Context())
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		raw, ok := inst.module.Memory().Read(uint32(results[0]>>32), uint32(results[0]))
		if !ok {
			t.Fatal("result out of bounds")
		}

		var got map[string]any
		if err := msgpack.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}

		return got
	}

	first := read("tenant-1", false)
	assertConfigFields(t, first, map[string]any{
		"vat_cert":     "VAT-1",
		"vat_found":    true,
		"soft_flag":    false,
		"soft_found":   false,
		"company_name": "Company 1",
		"address":      "Address 1",
		"tax_id":       "TIN-1",
		"logo_url":     "https://example.test/1.png",
		"logo_found":   true,
	})

	second := read("tenant-2", true)
	assertConfigFields(t, second, map[string]any{
		"vat_cert":     "VAT-2",
		"vat_found":    true,
		"soft_flag":    true,
		"soft_found":   true,
		"company_name": "Company 2",
		"address":      "Address 2",
		"tax_id":       "TIN-2",
		"logo_url":     "",
		"logo_found":   false,
	})

	store.values["tenant-1"]["l10n_gh.vat_cert_number"] = "VAT-1-updated"
	store.profiles["tenant-1"].Name = "Company 1 updated"
	store.profiles["tenant-1"].LogoURL = nil
	assertConfigFields(t, read("tenant-1", false), map[string]any{
		"vat_cert":     "VAT-1-updated",
		"vat_found":    true,
		"company_name": "Company 1 updated",
		"logo_url":     "",
		"logo_found":   false,
		"soft_flag":    false,
		"soft_found":   false,
	})
}

func assertConfigFields(t *testing.T, got, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want %v", key, got[key], value)
		}
	}
}
