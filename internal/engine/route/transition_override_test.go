package route

import (
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestRegisterRoutes_TransitionOverrideRetainsDeclaredGuards(t *testing.T) {
	transition := model.Transition("draft", "confirmed", "confirm").Requires(perm.Ref("sales:order:confirm")).Condition("record.name != ''")
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed", "cancelled").Workflow(
		transition, model.Transition("confirmed", "cancelled", "cancel"),
	))
	override := ExplicitRoutesFrom([]abiv1.RouteDeclaration{{
		Model:      "sales.order",
		Name:       "confirm",
		Auth:       "none",
		Transition: &abiv1.WorkflowTransitionRef{From: transition.From, To: transition.To},
	}})
	table := New()
	suppressed, err := RegisterRoutes(table, "sales", "domain", override, []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatal(err)
	}
	if len(suppressed) != 1 || suppressed[0].Op != "confirm" || suppressed[0].Kind != SuppressedWorkflowTransition {
		t.Fatalf("suppressed = %+v", suppressed)
	}

	entry, params, result, _ := table.Lookup("POST", "/sales/orders/order-1/confirm")
	if result != RouteFound || params["id"] != "order-1" {
		t.Fatalf("result=%v params=%v", result, params)
	}
	got := entry.Manifest
	if got.EngineNative || got.CrudAction != "workflow_transition" || got.Auth != "required" ||
		len(got.Permissions) != 1 || got.Permissions[0] != transition.Permission || got.PathParams["id"] != "uuid" {
		t.Fatalf("override manifest = %+v", got)
	}
	if got.Workflow == nil || got.Workflow.Field != "state" || got.Workflow.From != "draft" ||
		got.Workflow.To != "confirmed" || got.Workflow.Condition != transition.ConditionExpr {
		t.Fatalf("workflow = %+v", got.Workflow)
	}

	cancel, _, result, _ := table.Lookup("POST", "/sales/orders/order-1/cancel")
	if result != RouteFound || !cancel.Manifest.EngineNative {
		t.Fatal("unmodified transition must retain its engine-native handler")
	}
}

func TestRegisterRoutes_RejectsInvalidTransitionOverrides(t *testing.T) {
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed").Workflow(
		model.Transition("draft", "confirmed", "confirm"),
	))
	for _, tc := range []struct {
		name  string
		route ExplicitRoute
		want  string
	}{
		{
			name:  "empty action",
			route: ExplicitRoute{Transition: &abiv1.WorkflowTransitionRef{From: "draft", To: "confirmed"}},
			want:  "requires an action name",
		},
		{
			name:  "ordinary action",
			route: ExplicitRoute{Name: "confirm"},
			want:  "engine.HandleTransition",
		},
		{
			name: "unknown action",
			route: ExplicitRoute{
				Name:       "typo",
				Transition: &abiv1.WorkflowTransitionRef{From: "draft", To: "confirmed"},
			},
			want: "does not match",
		},
		{
			name: "different source",
			route: ExplicitRoute{
				Name:       "confirm",
				Transition: &abiv1.WorkflowTransitionRef{From: "confirmed", To: "confirmed"},
			},
			want: "does not match",
		},
		{
			name: "different target",
			route: ExplicitRoute{
				Name:       "confirm",
				Transition: &abiv1.WorkflowTransitionRef{From: "draft", To: "draft"},
			},
			want: "does not match",
		},
		{
			name: "collection",
			route: ExplicitRoute{
				Name:       "confirm",
				Scope:      "collection",
				Transition: &abiv1.WorkflowTransitionRef{From: "draft", To: "confirmed"},
			},
			want: "POST record action",
		},
		{
			name: "method",
			route: ExplicitRoute{
				Name:       "confirm",
				Method:     "GET",
				Transition: &abiv1.WorkflowTransitionRef{From: "draft", To: "confirmed"},
			},
			want: "POST record action",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.route.Model = "sales.order"
			table := New()
			_, err := RegisterRoutes(table, "sales", "domain", []ExplicitRoute{tc.route}, []model.ModelDeclaration{*md})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "sales.order") ||
				!strings.Contains(err.Error(), tc.route.Name) || !strings.Contains(err.Error(), `module "sales"`) {
				t.Fatalf("error = %v", err)
			}
			if len(table.All()) != 0 {
				t.Fatal("failed override left routes in the table")
			}
		})
	}
}

func TestRegisterRoutes_TransitionNameMatchingReservedVerbIsStillPostRecordAction(t *testing.T) {
	md := model.Define("order").Field("state", model.Selection("draft", "confirmed").Workflow(
		model.Transition("draft", "confirmed", "get"),
	))
	table := New()
	_, err := RegisterRoutes(table, "sales", "domain", []ExplicitRoute{{
		Model:      "sales.order",
		Name:       "get",
		Transition: &abiv1.WorkflowTransitionRef{From: "draft", To: "confirmed"},
	}}, []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatal(err)
	}
	if entry, _, result, _ := table.Lookup("POST", "/sales/orders/order-1/get"); result != RouteFound || entry.Manifest.EngineNative {
		t.Fatal("reserved name changed the transition's method or path")
	}
}
