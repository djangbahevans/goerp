package manifest

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func manifestWithViews(t *testing.T, views []map[string]any) []byte {
	t.Helper()
	fields := minimalManifestFields()
	fields["views"] = views
	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return m
}

func TestLoadManifest_CustomViewWithComponent_Passes(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "dashboard", "type": "custom", "resource": "sales.order", "label": "Dashboard", "component": "SalesDashboard"},
	})

	loaded, err := Load(m)
	if err != nil {
		t.Fatalf("expected a custom view with a component to load, got %v", err)
	}
	if got := loaded.Views[0].Component; got != "SalesDashboard" {
		t.Errorf("Views[0].Component = %q, want %q", got, "SalesDashboard")
	}
}

func TestLoadManifest_CustomViewWithoutComponent_Rejected(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "dashboard", "type": "custom", "resource": "sales.order", "label": "Dashboard"},
	})

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected a custom view with no component to be rejected")
	}
	if !strings.Contains(err.Error(), `view "dashboard"`) || !strings.Contains(err.Error(), "requires a non-empty component") {
		t.Errorf("expected error to name the view and the missing component, got: %v", err)
	}
}

func TestLoadManifest_NonCustomViewWithoutComponent_Passes(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "orders_list", "type": "list", "resource": "sales.order", "label": "Orders", "columns": []map[string]any{}, "filters": []map[string]any{}},
	})

	if _, err := Load(m); err != nil {
		t.Fatalf("expected a non-custom view with no component to load, got %v", err)
	}
}

func TestLoadManifest_CustomViewComponentRoundTrips(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "dashboard", "type": "custom", "resource": "sales.order", "label": "Dashboard", "component": "SalesDashboard"},
	})

	loaded, err := Load(m)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	out, err := json.Marshal(loaded.Views[0])
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	if !strings.Contains(string(out), `"component":"SalesDashboard"`) {
		t.Errorf("marshaled view = %s, want it to contain component", out)
	}
}
