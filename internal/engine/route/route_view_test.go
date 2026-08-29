package route

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func widgetModel() *model.ModelDeclaration {
	return model.Define("widget").
		EnableOps(model.List, model.Get, model.Create, model.Update, model.Delete).
		Field("name", model.Char().Required().Primary()).
		Field("is_active", model.Boolean()).
		Field("notes", model.Text().Computed("_compute_notes")).
		Field("tenant_id", model.UUID().Required()).
		Field("etag", model.Text().Required())
}

func TestSynthesizeViews_NoEnableViewsProducesNothing(t *testing.T) {
	md := model.Define("widget").EnableOps(model.List)
	views, suppressed, nav, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, nil, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	if len(views) != 0 || len(suppressed) != 0 || len(nav) != 0 {
		t.Fatalf("views=%v suppressed=%v nav=%v, want all empty", views, suppressed, nav)
	}
}

func TestSynthesizeViews_ListViewRequiresListOp(t *testing.T) {
	md := model.Define("widget").EnableViews(model.ListView)
	if _, _, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, nil, nil); err == nil {
		t.Fatal("want an error: EnableViews(ListView) without List in EnableOps")
	}
}

func TestSynthesizeViews_FormViewRequiresGetCreateUpdate(t *testing.T) {
	cases := []struct {
		name string
		ops  []model.Op
	}{
		{"none", nil},
		{"get only", []model.Op{model.Get}},
		{"get and create, missing update", []model.Op{model.Get, model.Create}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := model.Define("widget").EnableOps(tc.ops...).EnableViews(model.FormView)
			if _, _, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, nil, nil); err == nil {
				t.Fatal("want an error: EnableViews(FormView) without Get+Create+Update")
			}
		})
	}
}

func TestSynthesizeViews_NavRequiresListView(t *testing.T) {
	md := model.Define("widget").
		EnableOps(model.Get, model.Create, model.Update).
		EnableViews(model.FormView).
		Nav("Sales", "Widgets", 10)
	if _, _, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, nil, nil); err == nil {
		t.Fatal("want an error: .Nav() without EnableViews(ListView)")
	}
}

func TestSynthesizeViews_ListView_ColumnDerivationAndNaming(t *testing.T) {
	md := widgetModel().EnableViews(model.ListView)
	views, suppressed, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, nil, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	if len(suppressed) != 0 {
		t.Fatalf("suppressed = %v, want none", suppressed)
	}
	if len(views) != 1 {
		t.Fatalf("views = %v, want exactly one", views)
	}
	v := views[0]
	if v.Name != "testmodule_widget_list" {
		t.Errorf("Name = %q, want %q", v.Name, "testmodule_widget_list")
	}
	if v.Type != "list" {
		t.Errorf("Type = %q, want %q", v.Type, "list")
	}
	if v.Resource != "testmodule.widget" {
		t.Errorf("Resource = %q, want %q", v.Resource, "testmodule.widget")
	}

	wantColumns := map[string]struct {
		typ     string
		primary bool
	}{
		"name":      {"text", true},
		"is_active": {"boolean", false},
		"notes":     {"text", false},
	}
	if len(v.Columns) != len(wantColumns) {
		t.Fatalf("Columns = %v, want %d entries (tenant_id/etag excluded, standard fields)", v.Columns, len(wantColumns))
	}
	for _, col := range v.Columns {
		want, ok := wantColumns[col.Field]
		if !ok {
			t.Errorf("unexpected column %q", col.Field)
			continue
		}
		if col.Type != want.typ {
			t.Errorf("column %q: Type = %q, want %q", col.Field, col.Type, want.typ)
		}
		if col.Primary != want.primary {
			t.Errorf("column %q: Primary = %v, want %v", col.Field, col.Primary, want.primary)
		}
	}
}

func TestSynthesizeViews_FormView_FieldDerivation(t *testing.T) {
	md := widgetModel().EnableViews(model.FormView)
	views, _, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, nil, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("views = %v, want exactly one", views)
	}
	v := views[0]
	if v.Name != "testmodule_widget_form" || v.Type != "form" {
		t.Fatalf("Name/Type = %q/%q, want testmodule_widget_form/form", v.Name, v.Type)
	}
	if len(v.Sections) != 1 {
		t.Fatalf("Sections = %v, want exactly one", v.Sections)
	}

	byField := make(map[string]manifest.FormField, len(v.Sections[0].Fields))
	for _, f := range v.Sections[0].Fields {
		byField[f.Field] = f
	}

	if got := byField["name"]; !got.Required || got.Readonly {
		t.Errorf("name: Required=%v Readonly=%v, want Required=true Readonly=false", got.Required, got.Readonly)
	}
	if got := byField["notes"]; !got.Readonly {
		t.Error("notes: Readonly=false, want true (Computed field)")
	}
	if _, ok := byField["tenant_id"]; ok {
		t.Error("tenant_id should be excluded as a standard field")
	}
	if _, ok := byField["etag"]; ok {
		t.Error("etag should be excluded as a standard field")
	}
}

func TestSynthesizeViews_CreateActionOnlyWithCreateOpAndFormView(t *testing.T) {
	// List + Form + Create: action present.
	md := widgetModel().EnableViews(model.ListView, model.FormView)
	views, _, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, nil, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	list := findView(t, views, "testmodule_widget_list")
	if len(list.Actions) != 1 || list.Actions[0].Type != "create" || list.Actions[0].View != "testmodule_widget_form" {
		t.Fatalf("Actions = %v, want one create action targeting the form view", list.Actions)
	}

	// List only, no FormView: no action, since there's no form to link to.
	md2 := model.Define("gadget").EnableOps(model.List, model.Create).EnableViews(model.ListView)
	views2, _, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md2}, nil, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	list2 := findView(t, views2, "testmodule_gadget_list")
	if len(list2.Actions) != 0 {
		t.Fatalf("Actions = %v, want none (no FormView enabled)", list2.Actions)
	}
}

func TestSynthesizeViews_HandDeclaredViewWinsByName(t *testing.T) {
	md := widgetModel().EnableViews(model.ListView)
	existing := []manifest.View{{Name: "testmodule_widget_list", Type: "list", Resource: "testmodule.widget"}}
	views, suppressed, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, existing, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("views = %v, want none (dropped in favor of hand-declared)", views)
	}
	if len(suppressed) != 1 || suppressed[0] != (SuppressedView{Model: "testmodule.widget", View: "testmodule_widget_list"}) {
		t.Fatalf("suppressed = %v, want exactly [{testmodule.widget testmodule_widget_list}]", suppressed)
	}
}

func TestSynthesizeViews_HandDeclaredViewWinsByResourceAndType(t *testing.T) {
	md := widgetModel().EnableViews(model.ListView)
	// Different name, same (resource, type) pair.
	existing := []manifest.View{{Name: "widgets_overview", Type: "list", Resource: "testmodule.widget"}}
	views, suppressed, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, existing, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	if len(views) != 0 || len(suppressed) != 1 {
		t.Fatalf("views=%v suppressed=%v, want the synthesized view dropped", views, suppressed)
	}
}

func TestSynthesizeViews_CreateActionTargetsWinningFormViewOnResourceTypeCollision(t *testing.T) {
	md := widgetModel().EnableViews(model.ListView, model.FormView)
	existing := []manifest.View{{Name: "widget_edit_screen", Type: "form", Resource: "testmodule.widget"}}

	views, suppressed, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, existing, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	if len(suppressed) != 1 || suppressed[0].View != "testmodule_widget_form" {
		t.Fatalf("suppressed = %v, want the synthesized form dropped", suppressed)
	}
	list := findView(t, views, "testmodule_widget_list")
	if len(list.Actions) != 1 || list.Actions[0].View != "widget_edit_screen" {
		t.Fatalf("list.Actions = %v, want the create action targeting the winning hand-declared form %q, not the dropped synthesized name", list.Actions, "widget_edit_screen")
	}
}

func TestSynthesizeViews_NavTargetsWinningListViewOnResourceTypeCollision(t *testing.T) {
	md := widgetModel().EnableViews(model.ListView).Nav("Sales", "Widgets", 10)
	existing := []manifest.View{{Name: "widget_grid", Type: "list", Resource: "testmodule.widget"}}

	_, suppressed, nav, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, existing, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	if len(suppressed) != 1 || suppressed[0].View != "testmodule_widget_list" {
		t.Fatalf("suppressed = %v, want the synthesized list dropped", suppressed)
	}
	if len(nav) != 1 || len(nav[0].Children) != 1 || nav[0].Children[0].View != "widget_grid" {
		t.Fatalf("nav = %v, want the nav item targeting the winning hand-declared list %q, not the dropped synthesized name", nav, "widget_grid")
	}
}

func TestSynthesizeViews_TwoModelsSameDerivedViewNameErrors(t *testing.T) {
	// "foo.bar" and "foo_bar" both derive "testmodule_foo_bar_list" — the
	// underscore substitution isn't injective.
	md1 := model.Define("foo.bar").EnableOps(model.List).EnableViews(model.ListView)
	md2 := model.Define("foo_bar").EnableOps(model.List).EnableViews(model.ListView)

	if _, _, _, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md1, *md2}, nil, nil); err == nil {
		t.Fatal("SynthesizeViews: want an error when two models derive the same view name, got nil")
	}
}

func TestSynthesizeViews_NavMergesIntoExistingGroup(t *testing.T) {
	md1 := widgetModel().EnableViews(model.ListView).Nav("Sales", "Widgets", 10)
	md2 := model.Define("gadget", model.LabelPlural("Gadgets")).
		EnableOps(model.List).
		EnableViews(model.ListView).
		Nav("Sales", "Gadgets", 20)

	existingNav := []manifest.NavGroup{{Label: "Sales", Order: 5, Children: nil}}

	_, _, nav, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md1, *md2}, nil, existingNav)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	if len(nav) != 1 {
		t.Fatalf("nav = %v, want exactly one group (merged into the existing manifest-declared Sales group)", nav)
	}
	group := nav[0]
	if group.Order != 5 {
		t.Errorf("Order = %d, want 5 (manifest-declared order preserved, not overwritten)", group.Order)
	}
	if len(group.Children) != 2 {
		t.Fatalf("Children = %v, want both models' nav items", group.Children)
	}
	if group.Children[0].Label != "Widgets" || group.Children[0].View != "testmodule_widget_list" || group.Children[0].Route != "/testmodule/widgets" {
		t.Errorf("first child = %+v, unexpected", group.Children[0])
	}
	if group.Children[1].Label != "Gadgets" || group.Children[1].View != "testmodule_gadget_list" || group.Children[1].Route != "/testmodule/gadgets" {
		t.Errorf("second child = %+v, unexpected", group.Children[1])
	}
}

func TestSynthesizeViews_DoesNotMutateCallerOwnedNavigationSlice(t *testing.T) {
	md := widgetModel().EnableViews(model.ListView).Nav("Sales", "Widgets", 10)
	original := []manifest.NavGroup{{Label: "Sales", Order: 5, Children: []manifest.NavItem{{Label: "Existing", Route: "/sales/existing"}}}}

	_, _, nav, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, nil, original)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}

	if len(original[0].Children) != 1 || original[0].Children[0].Label != "Existing" {
		t.Fatalf("caller's original navigation slice was mutated: %+v", original)
	}
	if len(nav[0].Children) != 2 {
		t.Fatalf("returned nav = %v, want the original item plus the merged one", nav)
	}
}

func TestSynthesizeViews_NavCreatesNewGroupWhenNoneMatches(t *testing.T) {
	md := widgetModel().EnableViews(model.ListView).Nav("Sales", "Widgets", 10)
	_, _, nav, err := SynthesizeViews("testmodule", "domain", []model.ModelDeclaration{*md}, nil, nil)
	if err != nil {
		t.Fatalf("SynthesizeViews: %v", err)
	}
	if len(nav) != 1 || nav[0].Label != "Sales" || nav[0].Order != 10 {
		t.Fatalf("nav = %v, want one new Sales group at order 10", nav)
	}
}

func findView(t *testing.T, views []manifest.View, name string) manifest.View {
	t.Helper()
	for _, v := range views {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no view named %q in %v", name, views)
	return manifest.View{}
}
