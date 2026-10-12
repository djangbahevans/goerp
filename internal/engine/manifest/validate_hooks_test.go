package manifest

import (
	"encoding/json/v2"
	"slices"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestLoadHooks(t *testing.T) {
	for _, name := range []string{abi.HookRequestBefore, abi.HookRequestAfter, abi.HookTenantProvisioned, abi.HookModuleInstalled, abi.HookModuleUpgraded, abi.HookModuleUninstalled} {
		fields := minimalManifestFields()
		fields["hooks"] = []map[string]any{{"hook": name, "handler": "handler"}}
		data, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if loaded.Hooks[0].EffectivePriority() != 50 {
			t.Fatal("omitted priority must be 50")
		}
	}

	for _, tc := range []struct {
		name  string
		hooks []map[string]any
		valid bool
	}{
		{name: "explicit zero", hooks: []map[string]any{{"hook": abi.HookRequestBefore, "handler": "handler", "priority": 0}}, valid: true},
		{name: "negative priority", hooks: []map[string]any{{"hook": abi.HookRequestBefore, "handler": "handler", "priority": -10}}, valid: true},
		{name: "custom hook", hooks: []map[string]any{{"hook": "accounting.compute_taxes", "handler": "handler"}}, valid: true},
		{name: "unknown engine hook", hooks: []map[string]any{{"hook": "engine.request.typo", "handler": "handler"}}},
		{name: "missing hook", hooks: []map[string]any{{"handler": "handler"}}},
		{name: "missing handler", hooks: []map[string]any{{"hook": abi.HookRequestBefore}}},
		{name: "invalid handler", hooks: []map[string]any{{"hook": abi.HookRequestBefore, "handler": "handler name"}}},
		{name: "malformed hook", hooks: []map[string]any{{"hook": "accounting..taxes", "handler": "handler"}}},
		{name: "fractional priority", hooks: []map[string]any{{"hook": abi.HookRequestBefore, "handler": "handler", "priority": 1.5}}},
		{name: "duplicate", hooks: []map[string]any{{"hook": abi.HookRequestBefore, "handler": "handler"}, {"hook": abi.HookRequestBefore, "handler": "handler"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := minimalManifestFields()
			fields["hooks"] = tc.hooks
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(data)
			if (err == nil) != tc.valid {
				t.Fatalf("Load error = %v, valid = %v", err, tc.valid)
			}
			if tc.name == "explicit zero" && loaded.Hooks[0].EffectivePriority() != 0 {
				t.Fatal("explicit zero priority must stay zero")
			}
			if tc.name == "custom hook" && !slices.ContainsFunc(loaded.Diagnostics, func(d Diagnostic) bool { return d.Rule == RuleUnsupportedHook }) {
				t.Fatal("missing unsupported-hook diagnostic")
			}
		})
	}
}
