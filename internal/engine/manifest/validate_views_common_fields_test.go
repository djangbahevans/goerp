package manifest

import (
	"strings"
	"testing"
)

func TestLoadManifest_ViewDuplicateName_Rejected(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "orders_list", "type": "list", "resource": "sales.order", "label": "Orders", "columns": []map[string]any{}, "filters": []map[string]any{}},
		{"name": "orders_list", "type": "list", "resource": "sales.order", "label": "Orders Again", "columns": []map[string]any{}, "filters": []map[string]any{}},
	})

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected two views with the same name to be rejected")
	}
	if !strings.Contains(err.Error(), `view "orders_list"`) || !strings.Contains(err.Error(), "must be unique within this manifest's views") {
		t.Fatalf("expected error to name the duplicate view, got: %v", err)
	}
}

func TestLoadManifest_ViewUnknownType_Rejected(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "orders_grid", "type": "spreadsheet", "resource": "sales.order", "label": "Orders"},
	})

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected an unsupported view type to be rejected")
	}
	if !strings.Contains(err.Error(), `view "orders_grid"`) || !strings.Contains(err.Error(), `type "spreadsheet"`) {
		t.Fatalf("expected error to name the view and the invalid type, got: %v", err)
	}
}

func TestLoadManifest_ViewAllSevenTypes_Passes(t *testing.T) {
	views := []map[string]any{
		{"name": "v_list", "type": "list", "resource": "sales.order", "label": "L", "columns": []map[string]any{}, "filters": []map[string]any{}},
		{"name": "v_form", "type": "form", "resource": "sales.order", "label": "F"},
		{"name": "v_kanban", "type": "kanban", "resource": "sales.order", "label": "K"},
		{"name": "v_calendar", "type": "calendar", "resource": "sales.order", "label": "C"},
		{"name": "v_pivot", "type": "pivot", "resource": "sales.order", "label": "P"},
		{"name": "v_timeline", "type": "timeline", "resource": "sales.order", "label": "T"},
		{"name": "v_custom", "type": "custom", "resource": "sales.order", "label": "Cu", "component": "SalesDashboard"},
	}
	m := manifestWithViews(t, views)

	loaded, err := Load(m)
	if err != nil {
		t.Fatalf("expected a manifest with all seven view types to load, got %v", err)
	}
	if len(loaded.Views) != len(views) {
		t.Fatalf("Views = %d, want %d", len(loaded.Views), len(views))
	}
}

func TestLoadManifest_ViewResourceNotDotDelimited_Rejected(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "orders_list", "type": "list", "resource": "order", "label": "Orders"},
	})

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected a resource with no {module}.{resource} shape to be rejected")
	}
	if !strings.Contains(err.Error(), `view "orders_list"`) || !strings.Contains(err.Error(), "must be {module}.{resource}") {
		t.Fatalf("expected error to describe the required resource format, got: %v", err)
	}
}

func TestLoadManifest_ViewResourceThreeSegments_Rejected(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "orders_list", "type": "list", "resource": "sales.order.extra", "label": "Orders"},
	})

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected a three-segment resource to be rejected")
	}
	if !strings.Contains(err.Error(), "must be {module}.{resource}") {
		t.Fatalf("expected error to describe the required resource format, got: %v", err)
	}
}

func TestLoadManifest_ViewMissingName_Rejected(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"type": "list", "resource": "sales.order", "label": "Orders"},
	})

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected a view with no name to be rejected")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected error to mention the missing name, got: %v", err)
	}
}

func TestLoadManifest_ViewMissingLabel_Rejected(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "orders_list", "type": "list", "resource": "sales.order"},
	})

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected a view with no label to be rejected")
	}
	if !strings.Contains(err.Error(), `view "orders_list"`) || !strings.Contains(err.Error(), "label is required") {
		t.Fatalf("expected error to name the view and mention the missing label, got: %v", err)
	}
}

func TestLoadManifest_ViewNameNotAlphanumericUnderscore_Rejected(t *testing.T) {
	m := manifestWithViews(t, []map[string]any{
		{"name": "orders-list", "type": "list", "resource": "sales.order", "label": "Orders"},
	})

	_, err := Load(m)
	if err == nil {
		t.Fatalf("expected a hyphenated view name to be rejected")
	}
	if !strings.Contains(err.Error(), "name is required and must be alphanumeric/underscore") {
		t.Fatalf("expected error to describe the required name format, got: %v", err)
	}
}

func TestLoadManifest_NoViews_Passes(t *testing.T) {
	m := manifestWithViews(t, nil)
	if _, err := Load(m); err != nil {
		t.Fatalf("expected a manifest with no views to load, got %v", err)
	}
}
