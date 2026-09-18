package route

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestRegisterModelWorkflowActions_DerivesOneRoutePerTransition(t *testing.T) {
	table := New()
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed", "cancelled").Workflow(
		model.Transition("draft", "confirmed", "confirm").Requires("sales:order:confirm"),
		model.Transition("confirmed", "cancelled", "cancel"),
	))

	suppressed, err := RegisterModelWorkflowActions(table, "sales", "domain", []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatalf("RegisterModelWorkflowActions: %v", err)
	}
	if len(suppressed) != 0 {
		t.Fatalf("suppressed = %v, want none", suppressed)
	}

	entry, _, result, _ := table.Lookup("POST", "/sales/orders/{id}/confirm")
	if result != RouteFound {
		t.Fatalf("confirm: result = %v, want RouteFound", result)
	}
	if entry.Manifest.Model != "sales.order" {
		t.Fatalf("Model = %q, want %q", entry.Manifest.Model, "sales.order")
	}
	if entry.Manifest.Name != "confirm" {
		t.Fatalf("Name = %q, want %q", entry.Manifest.Name, "confirm")
	}
	if entry.Manifest.CrudAction != "workflow_transition" {
		t.Fatalf("CrudAction = %q, want %q", entry.Manifest.CrudAction, "workflow_transition")
	}
	if !entry.Manifest.EngineNative {
		t.Fatal("EngineNative = false, want true")
	}
	if len(entry.Manifest.Permissions) != 1 || entry.Manifest.Permissions[0] != "sales:order:confirm" {
		t.Fatalf("Permissions = %v, want [sales:order:confirm]", entry.Manifest.Permissions)
	}
	if entry.Manifest.Workflow == nil {
		t.Fatal("Workflow manifest is nil")
	}
	if entry.Manifest.Workflow.Field != "state" || entry.Manifest.Workflow.From != "draft" || entry.Manifest.Workflow.To != "confirmed" {
		t.Fatalf("Workflow manifest = %+v, want Field=state From=draft To=confirmed", entry.Manifest.Workflow)
	}

	cancelEntry, _, result, _ := table.Lookup("POST", "/sales/orders/{id}/cancel")
	if result != RouteFound {
		t.Fatalf("cancel: result = %v, want RouteFound", result)
	}
	if len(cancelEntry.Manifest.Permissions) != 0 {
		t.Fatalf("cancel Permissions = %v, want none (no .Requires() call)", cancelEntry.Manifest.Permissions)
	}
}

func TestRegisterModelWorkflowActions_ExplicitRouteSuppressesAutoDerived(t *testing.T) {
	table := New()
	if err := RegisterModuleRoutes(table, "sales", "domain", []ExplicitRoute{
		{Method: "POST", Path: "/orders/{id}/confirm"},
	}); err != nil {
		t.Fatalf("RegisterModuleRoutes: %v", err)
	}

	md := model.Define("order").Field("state", model.Selection("draft", "confirmed").Workflow(
		model.Transition("draft", "confirmed", "confirm"),
	))
	suppressed, err := RegisterModelWorkflowActions(table, "sales", "domain", []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatalf("RegisterModelWorkflowActions: %v", err)
	}

	want := SuppressedRoute{Model: "sales.order", Op: "confirm", Kind: SuppressedWorkflowTransition}
	if len(suppressed) != 1 || suppressed[0] != want {
		t.Fatalf("suppressed = %v, want exactly [%v]", suppressed, want)
	}

	entry, _, result, _ := table.Lookup("POST", "/sales/orders/{id}/confirm")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if entry.Manifest.EngineNative {
		t.Fatal("explicit route was overwritten by the auto-derived workflow transition")
	}
}

func TestRegisterModelWorkflowActions_NoWorkflowProducesZeroRoutes(t *testing.T) {
	table := New()
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed"))

	if _, err := RegisterModelWorkflowActions(table, "sales", "domain", []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterModelWorkflowActions: %v", err)
	}

	if _, _, result, _ := table.Lookup("POST", "/sales/orders/{id}/confirm"); result == RouteFound {
		t.Fatal("found a route for a model with no .Workflow() declaration")
	}
}

func TestRegisterRoutes_IncludesWorkflowTransitionSuppressions(t *testing.T) {
	table := New()
	if err := RegisterModuleRoutes(table, "sales", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/orders"},
		{Method: "POST", Path: "/orders/{id}/confirm"},
	}); err != nil {
		t.Fatalf("RegisterModuleRoutes: %v", err)
	}

	md := model.Define("order").
		EnableOps(model.List).
		Field("state", model.Selection("draft", "confirmed").Workflow(
			model.Transition("draft", "confirmed", "confirm"),
		))

	suppressed, err := RegisterRoutes(table, "sales", "domain", nil, []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	var sawEnableOps, sawWorkflow bool
	for _, s := range suppressed {
		if s.Op == "list" && s.Kind == "" {
			sawEnableOps = true
		}
		if s.Op == "confirm" && s.Kind == SuppressedWorkflowTransition {
			sawWorkflow = true
		}
	}
	if !sawEnableOps {
		t.Errorf("suppressed = %v, want an EnableOps(List) suppression", suppressed)
	}
	if !sawWorkflow {
		t.Errorf("suppressed = %v, want a workflow-transition suppression", suppressed)
	}
}
