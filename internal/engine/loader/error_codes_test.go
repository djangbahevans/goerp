package loader

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

func TestLoadModule_ErrorCodeValidation(t *testing.T) {
	rt := newTestRuntime(t)
	for _, code := range []string{"widgets.bad", "widgets.Resource.not_found", "other.widget.not_found"} {
		t.Run(code, func(t *testing.T) {
			src := Source{
				Name:          "widgets",
				ManifestBytes: manifestJSONWithFields(t, "widgets", okModule, nil, map[string]any{"error_codes": []string{code}}),
				WasmBytes:     okModule,
			}

			mod := LoadModule(t.Context(), rt, testPoolCfg(), src)

			if mod.Status != module.StatusFailed || !strings.Contains(mod.FailureReason, "error_codes[0]") {
				t.Fatalf("status = %v, reason = %q, want error code load failure", mod.Status, mod.FailureReason)
			}
			if mod.Pool != nil || mod.CompiledModule != nil {
				t.Fatal("invalid error codes must fail before compilation")
			}
		})
	}
}

func TestLoadModule_ErrorCodeRegistryReplacement(t *testing.T) {
	rt := newTestRuntime(t)
	reg := new(registry.ModuleRegistry)
	var previous *module.LoadedModule

	for _, codes := range [][]string{{"widgets.widget.not_found"}, {"widgets.widget.forbidden"}, nil} {
		src := Source{
			Name:          "widgets",
			ManifestBytes: manifestJSONWithFields(t, "widgets", okModule, nil, map[string]any{"error_codes": codes}),
			WasmBytes:     okModule,
		}
		mod := LoadModule(t.Context(), rt, testPoolCfg(), src)
		if mod.Status != module.StatusSyncing {
			t.Fatalf("load failed: %s", mod.FailureReason)
		}
		t.Cleanup(func() { mod.Pool.DrainAndClose(context.WithoutCancel(t.Context()), time.Second) })

		snap, err := reg.Update(map[string]*module.LoadedModule{"widgets": mod})
		if err != nil {
			t.Fatal(err)
		}

		current := snap.Modules()["widgets"]
		if len(current.ErrorCodes) != len(codes) {
			t.Fatalf("registered codes = %v, want %v", current.ErrorCodes, codes)
		}
		for _, code := range codes {
			if _, ok := current.ErrorCodes[code]; !ok {
				t.Errorf("missing registered code %q", code)
			}
		}
		if previous != nil {
			for code := range previous.ErrorCodes {
				if _, exists := current.ErrorCodes[code]; exists {
					t.Errorf("obsolete code %q survived replacement", code)
				}
			}
		}

		previous = current
	}
}
