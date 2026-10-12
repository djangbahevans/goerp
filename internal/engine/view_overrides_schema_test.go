package engine

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func overrideSchemaRegistry(t *testing.T) *registry.ModuleRegistry {
	t.Helper()
	decl := model.Define("contact").
		Field("name", model.Text().Required()).
		Field("note", model.Text()).
		EnableOps(model.List, model.Get, model.Create, model.Update).
		EnableViews(model.FormView, model.ListView)
	generated, _, _, err := route.SynthesizeViews("auto", "standard", []model.ModelDeclaration{*decl}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var shipped manifest.View
	if err := json.Unmarshal([]byte(`{
		"name":"contacts_form","type":"form","resource":"contacts.contact","label":"Contact",
		"sections":[{"name":"details","fields":[
			{"field":"name","label":"Name","required":true},
			{"field":"note","label":"Note","component_props":{"field":"note","label":"Private option"},"exact_options":{"limit":9007199254740993}}
		]}],
		"tabs":[{"label":"Extra","type":"fields","sections":[{"fields":[{"field":"note","label":"Tab note"}]}]}]
	}`), &shipped); err != nil {
		t.Fatal(err)
	}
	reg := &registry.ModuleRegistry{}
	_, err = reg.Update(map[string]*module.LoadedModule{
		"contacts": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Name: "contacts", Type: "standard", Views: []manifest.View{shipped}},
			ModelDecls: []model.ModelDeclaration{*decl},
		},
		"auto": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Name: "auto", Type: "standard", Views: generated},
			ModelDecls: []model.ModelDeclaration{*decl},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestSchemaWithViewOverrides_PreservesSnapshotAndIgnoresOrphans(t *testing.T) {
	base := overrideSchemaRegistry(t).Snapshot().SchemaResponse()
	before, err := json.Marshal(base, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	var beforeMembers any
	if err := json.Unmarshal(before, &beforeMembers); err != nil {
		t.Fatal(err)
	}
	rows := []viewOverrideResponse{
		{ViewName: "contacts_form", Element: "note", Property: "label", Value: jsontext.Value(`"Notes"`)},
		{ViewName: "contacts_form", Element: "note", Property: "order", Value: jsontext.Value(`0`)},
		{ViewName: "contacts_form", Element: "details", Property: "label", Value: jsontext.Value(`"Profile"`)},
		{ViewName: "contacts_form", Element: "Extra", Property: "label", Value: jsontext.Value(`"More"`)},
		{ViewName: "contacts_form", Element: "removed", Property: "label", Value: jsontext.Value(`"Gone"`)},
		{ViewName: "removed_view", Element: "note", Property: "label", Value: jsontext.Value(`"Gone"`)},
	}
	patched, err := schemaWithViewOverrides(base, rows)
	if err != nil {
		t.Fatal(err)
	}
	view := patched.Modules["contacts"].Views[0]
	if view.Sections[0].Fields[0].Field != "note" || view.Sections[0].Fields[0].Label != "Notes" || view.Sections[0].Label != "Profile" || view.Tabs[0].Label != "More" {
		t.Fatalf("patched view = %+v", view)
	}
	if got := view.Sections[0].Fields[0].ComponentProps["label"]; got != "Private option" {
		t.Fatalf("component options were patched: %v", got)
	}
	if got := string(view.Sections[0].Fields[0].Extra["exact_options"]); got != `{"limit":9007199254740993}` {
		t.Fatalf("unmodeled integer changed: %s", got)
	}
	if view.Tabs[0].Sections[0].Fields[0].Label != "Notes" {
		t.Fatal("nested tab field was not patched")
	}
	after, err := json.Marshal(base, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	var afterMembers any
	if err := json.Unmarshal(after, &afterMembers); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeMembers, afterMembers) {
		t.Fatalf("shared snapshot mutated: %v", err)
	}
	if !reflect.DeepEqual(base.Modules["contacts"].Models, patched.Modules["contacts"].Models) || !reflect.DeepEqual(base.Modules["contacts"].Routes, patched.Modules["contacts"].Routes) {
		t.Fatal("overrides changed model or route declarations")
	}
	if patched.SchemaHash == base.SchemaHash {
		t.Fatal("override did not change schema hash")
	}
	again, err := schemaWithViewOverrides(base, rows)
	if err != nil || again.SchemaHash != patched.SchemaHash {
		t.Fatalf("schema hash is not deterministic: %v", err)
	}
	orphaned, err := schemaWithViewOverrides(base, rows[4:])
	if err != nil || orphaned != base {
		t.Fatalf("orphaned overrides changed schema: %v", err)
	}
}

func (f *dispatchSavedFiltersFixture) postOverride(t *testing.T, body string, wantStatus int) viewOverrideResponse {
	t.Helper()
	w := httptest.NewRecorder()
	f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodPost, "/_meta/view-overrides", body, true, ""))
	if w.Code != wantStatus {
		t.Fatalf("POST %s = %d %s, want %d", body, w.Code, w.Body, wantStatus)
	}
	var row viewOverrideResponse
	if wantStatus < 300 {
		if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
	}
	return row
}

func (f *dispatchSavedFiltersFixture) readOverrideSchema(t *testing.T) registry.SchemaResponse {
	t.Helper()
	w := httptest.NewRecorder()
	f.e.dispatchSchemaRoute(w, f.overrideRequest(http.MethodGet, "/_meta/schema", "", false, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("schema = %d %s", w.Code, w.Body)
	}
	var out registry.SchemaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDispatchSchemaRoute_ViewOverridesTenantIsolationAndDeletion(t *testing.T) {
	f := newViewOverridesFixture(t)
	f.e.moduleRegistry = overrideSchemaRegistry(t)
	other := newViewOverridesFixture(t)
	other.e.moduleRegistry = f.e.moduleRegistry
	base := f.readOverrideSchema(t)

	for _, viewName := range []string{"contacts_form", "auto_contact_form"} {
		for property, value := range map[string]string{
			"label":       `"Customer name"`,
			"required":    `false`,
			"order":       `5`,
			"help_text":   `"Legal name"`,
			"placeholder": `"Enter name"`,
		} {
			f.postOverride(t, `{"view_name":"`+viewName+`","element":"name","property":"`+property+`","value":`+value+`}`, http.StatusCreated)
		}
		f.postOverride(t, `{"view_name":"`+viewName+`","element":"name","property":"hidden","value":true}`, http.StatusCreated)
	}
	patched := f.readOverrideSchema(t)
	for _, moduleName := range []string{"contacts", "auto"} {
		var form manifest.View
		for _, view := range patched.Modules[moduleName].Views {
			if view.Type == "form" {
				form = view
			}
		}
		fields := form.Sections[0].Fields
		name := fields[1]
		if name.Field != "name" || name.Label != "Customer name" || !name.Hidden || name.Required || name.HelpText != "Legal name" || name.Placeholder != "Enter name" {
			t.Fatalf("%s form field = %+v", moduleName, name)
		}
	}
	if foreign := other.readOverrideSchema(t); !reflect.DeepEqual(base, foreign) {
		t.Fatal("overrides leaked to another tenant")
	}
	f.postOverride(t, `{"view_name":"missing","element":"gone","property":"label","value":"Gone"}`, http.StatusCreated)
	f.postOverride(t, `{"view_name":"contacts_form","element":"gone","property":"label","value":"Gone"}`, http.StatusCreated)
	if withOrphans := f.readOverrideSchema(t); !reflect.DeepEqual(patched, withOrphans) {
		t.Fatal("orphaned overrides changed schema")
	}

	rows, err := loadViewOverrides(t.Context(), f.e.primaryDB, f.slug)
	if err != nil {
		t.Fatal(err)
	}
	// Unhide before deleting the exemption that allows a model-required field to be hidden.
	for _, row := range rows {
		if row.Property != "hidden" {
			continue
		}
		deleteOverride(t, f, row.ID, http.StatusNoContent)
	}
	for _, row := range rows {
		if row.Property == "hidden" {
			continue
		}
		deleteOverride(t, f, row.ID, http.StatusNoContent)
	}
	if restored := f.readOverrideSchema(t); !reflect.DeepEqual(base, restored) {
		t.Fatal("deletion did not restore declared schema and hash")
	}
}

func deleteOverride(t *testing.T, f *dispatchSavedFiltersFixture, id string, wantStatus int) {
	t.Helper()
	w := httptest.NewRecorder()
	f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodDelete, "/_meta/view-overrides/"+id, "", true, id))
	if w.Code != wantStatus {
		t.Fatalf("DELETE = %d %s, want %d", w.Code, w.Body, wantStatus)
	}
}

func TestDispatchViewOverrides_RequiredGuardAndAtomicCompanion(t *testing.T) {
	f := newViewOverridesFixture(t)
	f.e.moduleRegistry = overrideSchemaRegistry(t)
	body := `{"view_name":"contacts_form","element":"name","property":"hidden","value":true}`
	w := httptest.NewRecorder()
	f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodPost, "/_meta/view-overrides", body, true, ""))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "name") {
		t.Fatalf("hide required = %d %s", w.Code, w.Body)
	}
	rows, err := loadViewOverrides(t.Context(), f.e.primaryDB, f.slug)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected write persisted: %+v, %v", rows, err)
	}

	hidden := f.postOverride(t, `{"view_name":"contacts_form","element":"name","property":"hidden","value":true,"required":false}`, http.StatusCreated)
	rows, err = loadViewOverrides(t.Context(), f.e.primaryDB, f.slug)
	if err != nil || len(rows) != 2 {
		t.Fatalf("atomic companion = %+v, %v", rows, err)
	}
	var requiredID string
	for _, row := range rows {
		if row.Property == "required" {
			requiredID = row.ID
		}
	}
	deleteOverride(t, f, requiredID, http.StatusBadRequest)
	f.postOverride(t, `{"view_name":"contacts_form","element":"name","property":"required","value":true}`, http.StatusBadRequest)
	field := f.readOverrideSchema(t).Modules["contacts"].Views[0].Sections[0].Fields[0]
	if !field.Hidden || field.Required {
		t.Fatalf("rejected mutation changed schema: %+v", field)
	}
	deleteOverride(t, f, hidden.ID, http.StatusNoContent)
	deleteOverride(t, f, requiredID, http.StatusNoContent)

	for _, body := range []string{
		`{"view_name":"contacts_form","element":"name","property":"label","value":false}`,
		`{"view_name":"contacts_form","element":"name","property":"hidden","value":"true"}`,
		`{"view_name":"contacts_form","element":"name","property":"required","value":null}`,
		`{"view_name":"contacts_form","element":"name","property":"order","value":1.5}`,
		`{"view_name":"contacts_form","element":"name","property":"order","value":-1}`,
		`{"view_name":"contacts_form","element":"name","property":"label","value":"Name","required":false}`,
		`{"view_name":"contacts_form","element":"name","property":"hidden","value":true,"required":true}`,
		`{"view_name":"contacts_form","element":"name","property":"hidden","value":true,"required":null}`,
	} {
		f.postOverride(t, body, http.StatusBadRequest)
	}
}

func TestDispatchViewOverrides_ConcurrentWritesPreserveRequiredGuard(t *testing.T) {
	f := newViewOverridesFixture(t)
	f.e.moduleRegistry = overrideSchemaRegistry(t)
	f.postOverride(t, `{"view_name":"contacts_form","element":"name","property":"required","value":false}`, http.StatusCreated)
	requests := []*http.Request{
		f.overrideRequest(http.MethodPost, "/_meta/view-overrides", `{"view_name":"contacts_form","element":"name","property":"hidden","value":true}`, true, ""),
		f.overrideRequest(http.MethodPost, "/_meta/view-overrides", `{"view_name":"contacts_form","element":"name","property":"required","value":true}`, true, ""),
	}
	responses := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, req := range requests {
		wg.Go(func() {
			<-start
			f.e.dispatchViewOverridesRoute(responses[i], req)
		})
	}
	close(start)
	wg.Wait()
	accepted, rejected := 0, 0
	for _, response := range responses {
		switch response.Code {
		case http.StatusOK, http.StatusCreated:
			accepted++
		case http.StatusBadRequest:
			rejected++
		default:
			t.Fatalf("concurrent write = %d %s", response.Code, response.Body)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("concurrent writes accepted %d, rejected %d", accepted, rejected)
	}
	field := f.readOverrideSchema(t).Modules["contacts"].Views[0].Sections[0].Fields[0]
	if field.Hidden && field.Required {
		t.Fatalf("concurrent writes produced hidden required field: %+v", field)
	}
}

func TestDispatchViewOverrides_CompanionFailureRollsBackBothProperties(t *testing.T) {
	f := newViewOverridesFixture(t)
	f.e.moduleRegistry = overrideSchemaRegistry(t)
	if _, err := f.e.primaryDB.ExecContext(t.Context(), "ALTER TABLE tenant_"+f.slug+".view_overrides ADD CHECK (property <> 'required')"); err != nil {
		t.Fatal(err)
	}
	f.postOverride(t, `{"view_name":"contacts_form","element":"name","property":"hidden","value":true,"required":false}`, http.StatusInternalServerError)
	rows, err := loadViewOverrides(t.Context(), f.e.primaryDB, f.slug)
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed companion persisted rows: %+v, %v", rows, err)
	}
}

func TestDispatchViewOverrides_HiddenFieldRemainsReadableThroughAPI(t *testing.T) {
	f := newDispatchORMFixture(t)
	for _, group := range enginetables.Groups {
		if len(group.Tables) == 1 && group.Tables[0].Name == "view_overrides" {
			if err := group.Create(t.Context(), f.e.primaryDB, f.slug); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := f.e.moduleRegistry.UpdateWith(func(modules map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		mod := *modules["testmodule"]
		mod.Manifest.Views = []manifest.View{{
			Name: "widgets_form", Type: "form", Resource: "testmodule.widget", Label: "Widget",
			Sections: []manifest.FormSection{{Fields: []manifest.FormField{{Field: "name", Required: true}}}},
		}}
		modules["testmodule"] = &mod
		return modules, nil
	}); err != nil {
		t.Fatal(err)
	}
	overrides := &dispatchSavedFiltersFixture{e: f.e, slug: f.slug, tenantID: f.tenantID, userID: "00000000-0000-0000-0000-0000000000aa"}
	overrides.postOverride(t, `{"view_name":"widgets_form","element":"name","property":"hidden","value":true,"required":false}`, http.StatusCreated)
	field := overrides.readOverrideSchema(t).Modules["testmodule"].Views[0].Sections[0].Fields[0]
	if !field.Hidden {
		t.Fatal("field is not hidden in schema")
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", []byte(`{"name":"Visible through API"}`), f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets/"+id, nil, f.entryGet, map[string]string{"id": id}))
	var fetched map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &fetched); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || fetched["name"] != "Visible through API" {
		t.Fatalf("read hidden field = %d %s", w.Code, w.Body)
	}
}

func TestSchemaWithViewOverrides_ListColumnsAndSubListRequiredFields(t *testing.T) {
	base := overrideSchemaRegistry(t).Snapshot().SchemaResponse()
	rows := []viewOverrideResponse{
		{ViewName: "auto_contact_list", Element: "note", Property: "label", Value: jsontext.Value(`"Notes"`)},
		{ViewName: "auto_contact_list", Element: "note", Property: "hidden", Value: jsontext.Value(`true`)},
		{ViewName: "auto_contact_list", Element: "note", Property: "order", Value: jsontext.Value(`0`)},
	}
	patched, err := schemaWithViewOverrides(base, rows)
	if err != nil {
		t.Fatal(err)
	}
	var list manifest.View
	for _, view := range patched.Modules["auto"].Views {
		if view.Type == "list" {
			list = view
		}
	}
	if len(list.Columns) != 2 || list.Columns[0].Field != "note" || list.Columns[0].Label != "Notes" || !list.Columns[0].Hidden {
		t.Fatalf("patched columns = %+v", list.Columns)
	}

	nested := &registry.SchemaResponse{Modules: map[string]*registry.SchemaModule{
		"orders": {
			Models: map[string]registry.SchemaModel{
				"orders.order": {Fields: []registry.SchemaField{{Name: "lines", RelatedModel: "orders.line"}}},
				"orders.line":  {Fields: []registry.SchemaField{{Name: "description", Required: true}}},
			},
			Views: []manifest.View{{
				Name: "orders_form", Resource: "orders.order", Type: "form",
				Sections: []manifest.FormSection{{
					Type: "sub_list", Field: "lines",
					Columns: []manifest.ListColumn{{Field: "description"}},
				}},
			}},
		},
	}}
	rows = []viewOverrideResponse{{ViewName: "orders_form", Element: "description", Property: "hidden", Value: jsontext.Value(`true`)}}
	if failure := validateViewOverrideState(nested, rows, "orders_form", "description"); failure == nil || !strings.Contains(failure.message, "description") {
		t.Fatalf("required child field was not rejected: %+v", failure)
	}
	rows = append(rows, viewOverrideResponse{ViewName: "orders_form", Element: "description", Property: "required", Value: jsontext.Value(`false`)})
	if failure := validateViewOverrideState(nested, rows, "orders_form", "description"); failure != nil {
		t.Fatalf("child field exemption was rejected: %+v", failure)
	}
}
