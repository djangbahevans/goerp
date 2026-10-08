package loader

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

func TestLoadModule_EnableViewsAndNav_MergesIntoManifest(t *testing.T) {
	wasmBytes := compileFixture(t, "viewsfixture")
	rt := newRealFixtureRuntime(t)

	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", wasmBytes, []string{}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(t.Context(), rt, testPoolCfg(), src)
	if m.Status == module.StatusFailed {
		t.Fatalf("Status = StatusFailed, FailureReason = %q", m.FailureReason)
	}
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })

	byName := make(map[string]bool, len(m.Manifest.Views))
	for _, v := range m.Manifest.Views {
		byName[v.Name] = true
	}
	if !byName["widgets_widget_list"] {
		t.Errorf("Manifest.Views = %v, want widgets_widget_list", m.Manifest.Views)
	}
	if !byName["widgets_widget_form"] {
		t.Errorf("Manifest.Views = %v, want widgets_widget_form", m.Manifest.Views)
	}

	if len(m.Manifest.Navigation) != 1 || m.Manifest.Navigation[0].Label != "Sales" {
		t.Fatalf("Manifest.Navigation = %v, want one Sales group", m.Manifest.Navigation)
	}
	children := m.Manifest.Navigation[0].Children
	if len(children) != 1 || children[0].Label != "Widgets" || children[0].View != "widgets_widget_list" || children[0].Route != "/widgets" {
		t.Fatalf("Navigation[0].Children = %v, unexpected", children)
	}
}

func TestLoadModule_EnableViews_ValidationFailure_FailsLoad(t *testing.T) {
	wasmBytes := compileFixture(t, "viewsfixture_listviolation")
	rt := newRealFixtureRuntime(t)

	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", wasmBytes, []string{}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(t.Context(), rt, testPoolCfg(), src)
	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
	if m.FailureReason == "" {
		t.Error("expected a non-empty FailureReason")
	}
}

func TestLoadModule_EnableViews_HandDeclaredViewSuppressesSynthesized(t *testing.T) {
	wasmBytes := compileFixture(t, "viewsfixture")
	rt := newRealFixtureRuntime(t)

	handDeclared := map[string]any{
		"name":     "widgets_widget_list",
		"type":     "list",
		"resource": "widgets.widget",
		"label":    "All Widgets",
	}
	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSONWithFields(t, "widgets", wasmBytes, []string{}, map[string]any{"views": []any{handDeclared}}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(t.Context(), rt, testPoolCfg(), src)
	if m.Status == module.StatusFailed {
		t.Fatalf("Status = StatusFailed, FailureReason = %q", m.FailureReason)
	}
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })

	var matches int
	for _, v := range m.Manifest.Views {
		if v.Name == "widgets_widget_list" {
			matches++
			if v.Label != "All Widgets" {
				t.Errorf("Label = %q, want the hand-declared label to survive, not a synthesized one", v.Label)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("found %d views named widgets_widget_list, want exactly 1 (the hand-declared one, synthesized dropped)", matches)
	}
}
