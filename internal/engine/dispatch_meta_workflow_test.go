package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// newWorkflowFixtureEngine mirrors newSchemaFixtureEngine but declares a
// state field with .Workflow() transitions instead — goerp#864's
// /_meta/schema exposure and its auto-registered POST {plural}/{id}/
// {action_name} routes are the two things this fixture exercises.
func newWorkflowFixtureEngine(t *testing.T) *Engine {
	t.Helper()

	orderModel := model.Define("order").WithStandardFields().
		Field("state", model.Selection("draft", "confirmed", "cancelled").
			Default("draft").
			Workflow(
				model.Transition("draft", "confirmed", "confirm").Requires("sales:order:confirm"),
				model.Transition("confirmed", "cancelled", "cancel").
					Condition("record.amount_paid = 0"),
			))

	loadedModules := map[string]*module.LoadedModule{
		"sales": {
			Status: module.StatusReady,
			Manifest: manifest.Manifest{
				Name:        "sales",
				DisplayName: "Sales",
				Type:        "standard",
				Version:     "1.0.0",
			},
			ModelDecls: []model.ModelDeclaration{*orderModel},
		},
	}

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(loadedModules); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}

	return &Engine{moduleRegistry: reg}
}

func TestDispatchSchemaRoute_ExposesWorkflowTransitions(t *testing.T) {
	e := newWorkflowFixtureEngine(t)

	w := httptest.NewRecorder()
	e.dispatchSchemaRoute(w, schemaRequest(http.MethodGet, "/_meta/schema"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp metaSchemaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	mod, ok := resp.Modules["sales"]
	if !ok {
		t.Fatalf("modules missing \"sales\", got %v", resp.Modules)
	}
	md, ok := mod.Models["sales.order"]
	if !ok {
		t.Fatalf("models missing \"sales.order\", got %v", mod.Models)
	}

	var stateField *metaSchemaField
	for i, f := range md.Fields {
		if f.Name == "state" {
			stateField = &md.Fields[i]
		}
	}
	if stateField == nil {
		t.Fatalf("no state field in %+v", md.Fields)
	}
	if stateField.Workflow == nil {
		t.Fatal("state field's workflow is nil")
	}
	if len(stateField.Workflow.States) != 3 {
		t.Errorf("states = %v, want 3 entries", stateField.Workflow.States)
	}
	if len(stateField.Workflow.Transitions) != 2 {
		t.Fatalf("transitions = %+v, want 2 entries", stateField.Workflow.Transitions)
	}

	confirm := stateField.Workflow.Transitions[0]
	if confirm.From != "draft" || confirm.To != "confirmed" || confirm.ActionName != "confirm" {
		t.Errorf("confirm transition = %+v, want From=draft To=confirmed ActionName=confirm", confirm)
	}
	if confirm.Permission != "sales:order:confirm" {
		t.Errorf("confirm Permission = %q, want sales:order:confirm", confirm.Permission)
	}

	cancel := stateField.Workflow.Transitions[1]
	if cancel.Condition != "record.amount_paid = 0" {
		t.Errorf("cancel Condition = %q, want the declared expression", cancel.Condition)
	}

	var confirmRoute *metaSchemaRoute
	for i, r := range mod.Routes {
		if r.Name == "confirm" {
			confirmRoute = &mod.Routes[i]
		}
	}
	if confirmRoute == nil {
		t.Fatalf("no confirm action route in %+v", mod.Routes)
	}
	if confirmRoute.Method != "POST" || confirmRoute.Path != "/sales/orders/{id}/confirm" {
		t.Errorf("confirm route = %+v, want POST /sales/orders/{id}/confirm", confirmRoute)
	}
	if confirmRoute.CrudAction != "workflow_transition" {
		t.Errorf("confirm route crud_action = %q, want workflow_transition", confirmRoute.CrudAction)
	}
	if len(confirmRoute.Permissions) != 1 || confirmRoute.Permissions[0] != "sales:order:confirm" {
		t.Errorf("confirm route permissions = %v, want [sales:order:confirm]", confirmRoute.Permissions)
	}
}

func TestMetaSchemaWorkflowFrom_NoWorkflowIsNil(t *testing.T) {
	if got := metaSchemaWorkflowFrom(model.Char()); got != nil {
		t.Errorf("metaSchemaWorkflowFrom(Char()) = %+v, want nil", got)
	}
	if got := metaSchemaWorkflowFrom(model.Selection("a", "b")); got != nil {
		t.Errorf("metaSchemaWorkflowFrom(no .Workflow()) = %+v, want nil", got)
	}
}
