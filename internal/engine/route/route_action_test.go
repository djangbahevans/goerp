package route

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func actionRoute(modelName, name string) ExplicitRoute {
	crud := ""
	switch name {
	case "list", "get", "create", "update", "delete", "preview", "pivot":
		crud = name
	}
	return ExplicitRoute{Model: modelName, Name: name, CrudAction: crud, Auth: "required"}
}

func TestRegisterRoutes_ActionOverridesEnableOpsByIdentity(t *testing.T) {
	cases := []struct {
		name     string
		md       *model.ModelDeclaration
		wantPath string
	}{
		{"LabelPlural", model.Define("widget", model.LabelPlural("Sales Orders")).EnableOps(model.List), "/testmodule/sales-orders"},
		{"RoutePrefix", model.Define("widget").RoutePrefix("/gadgets").EnableOps(model.List), "/testmodule/gadgets"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := New()
			explicit := []ExplicitRoute{actionRoute("testmodule.widget", "list")}

			suppressed, err := RegisterRoutes(table, "testmodule", "domain", explicit, []model.ModelDeclaration{*tc.md})
			if err != nil {
				t.Fatalf("RegisterRoutes: %v", err)
			}
			if len(suppressed) != 1 || suppressed[0] != (SuppressedRoute{Model: "testmodule.widget", Op: "list"}) {
				t.Fatalf("suppressed = %v, want exactly [{testmodule.widget list}]", suppressed)
			}

			entry, _, result, _ := table.Lookup("GET", tc.wantPath)
			if result != RouteFound {
				t.Fatalf("GET %s: result = %v, want RouteFound", tc.wantPath, result)
			}
			if entry.Manifest.EngineNative || entry.Manifest.Name != "list" || entry.Manifest.CrudAction != "list" {
				t.Fatalf("entry manifest = %+v, want the module's explicit list action", entry.Manifest)
			}

			var lists int
			for _, r := range table.All() {
				if r.Method == "GET" && r.Entry.Manifest.CrudAction == "list" {
					lists++
				}
			}
			if lists != 1 {
				t.Fatalf("GET list routes = %d, want 1", lists)
			}
		})
	}
}

func TestRegisterRoutes_ActionPathIgnoresDeclaredPath(t *testing.T) {
	table := New()
	md := model.Define("widget", model.LabelPlural("Sales Orders")).EnableOps(model.List)
	r := actionRoute("testmodule.widget", "list")
	r.Path = "/widgets"

	if _, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{r}, []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	if _, _, result, _ := table.Lookup("GET", "/testmodule/widgets"); result == RouteFound {
		t.Fatal("found a route at the path the SDK declared")
	}
	if _, _, result, _ := table.Lookup("GET", "/testmodule/sales-orders"); result != RouteFound {
		t.Fatal("no route at the engine-derived path")
	}
}

func TestRegisterRoutes_CustomActionUsesModelPlural(t *testing.T) {
	table := New()
	md := model.Define("widget", model.LabelPlural("Sales Orders"))

	if _, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{actionRoute("testmodule.widget", "confirm")}, []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	entry, params, result, _ := table.Lookup("POST", "/testmodule/sales-orders/abc/confirm")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if params["id"] != "abc" {
		t.Fatalf("params = %v, want id=abc", params)
	}
	if entry.Manifest.Name != "confirm" || entry.Manifest.CrudAction != "" {
		t.Fatalf("manifest = %+v, want Name=confirm CrudAction=\"\"", entry.Manifest)
	}
}

func TestRegisterRoutes_ActionDerivesEveryReservedVerb(t *testing.T) {
	table := New()
	md := model.Define("widget")
	verbs := []struct{ name, method, path string }{
		{"list", "GET", "/testmodule/widgets"},
		{"get", "GET", "/testmodule/widgets/{id}"},
		{"create", "POST", "/testmodule/widgets"},
		{"update", "PUT", "/testmodule/widgets/{id}"},
		{"delete", "DELETE", "/testmodule/widgets/{id}"},
		{"preview", "POST", "/testmodule/widgets/preview"},
		{"pivot", "GET", "/testmodule/widgets/pivot"},
	}
	var explicit []ExplicitRoute
	for _, v := range verbs {
		explicit = append(explicit, actionRoute("testmodule.widget", v.name))
	}

	if _, err := RegisterRoutes(table, "testmodule", "domain", explicit, []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	for _, v := range verbs {
		entry, _, result, _ := table.Lookup(v.method, v.path)
		if result != RouteFound {
			t.Fatalf("%s %s: result = %v, want RouteFound", v.method, v.path, result)
		}
		if entry.Manifest.CrudAction != v.name {
			t.Fatalf("%s %s: CrudAction = %q, want %q", v.method, v.path, entry.Manifest.CrudAction, v.name)
		}
	}
}

func TestRegisterRoutes_ActionIrregularPlural(t *testing.T) {
	table := New()
	md := model.Define("person")

	if _, err := RegisterRoutes(table, "hr", "domain", []ExplicitRoute{actionRoute("hr.person", "list")}, []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if _, _, result, _ := table.Lookup("GET", "/hr/people"); result != RouteFound {
		t.Fatalf("result = %v, want RouteFound at /hr/people", result)
	}
}

func TestRegisterRoutes_ActionSuppressesWorkflowTransitionByIdentity(t *testing.T) {
	table := New()
	md := model.Define("widget", model.LabelPlural("Sales Orders")).Field("state", model.Selection("draft", "confirmed").Workflow(
		model.Transition("draft", "confirmed", "confirm")))

	suppressed, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{actionRoute("testmodule.widget", "confirm")}, []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if len(suppressed) != 1 || suppressed[0].Kind != SuppressedWorkflowTransition {
		t.Fatalf("suppressed = %v, want one suppressed workflow transition", suppressed)
	}
}

func TestRegisterRoutes_ActionForUndeclaredModelFails(t *testing.T) {
	cases := []struct {
		name   string
		models []model.ModelDeclaration
	}{
		{"no models", nil},
		{"other model", []model.ModelDeclaration{*model.Define("gadget")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := New()
			_, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{actionRoute("testmodule.widget", "list")}, tc.models)
			if err == nil {
				t.Fatal("RegisterRoutes succeeded, want an error")
			}
			for _, want := range []string{`"testmodule"`, `"testmodule.widget"`} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not name %s", err, want)
				}
			}
		})
	}
}

func TestRegisterRoutes_PathRouteStillSuppressesByPath(t *testing.T) {
	table := New()
	md := model.Define("widget", model.LabelPlural("Sales Orders")).EnableOps(model.List)

	suppressed, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{{Method: "GET", Path: "/sales-orders"}}, []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if len(suppressed) != 1 {
		t.Fatalf("suppressed = %v, want one", suppressed)
	}
}

func TestRegisterRoutes_ActionMatchesModuleQualifiedDeclarationName(t *testing.T) {
	table := New()
	md := model.Define("testmodule.widget", model.LabelPlural("Sales Orders"))

	if _, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{actionRoute("testmodule.widget", "list")}, []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if _, _, result, _ := table.Lookup("GET", "/testmodule/sales-orders"); result != RouteFound {
		t.Fatalf("result = %v, want RouteFound at /testmodule/sales-orders", result)
	}
}

func TestRegisterRoutes_ActionSuppressesEnableOpsForModuleQualifiedDeclarationName(t *testing.T) {
	table := New()
	md := model.Define("testmodule.widget", model.LabelPlural("Sales Orders")).EnableOps(model.List)

	suppressed, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{actionRoute("testmodule.widget", "list")}, []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if len(suppressed) != 1 {
		t.Fatalf("suppressed = %v, want one", suppressed)
	}
}

func lookupAction(t *testing.T, table *RouteTable, method, path string) *RouteEntry {
	t.Helper()
	entry, _, result, _ := table.Lookup(method, path)
	if result != RouteFound {
		t.Fatalf("%s %s: result = %v, want RouteFound", method, path, result)
	}
	return entry
}

func TestRegisterRoutes_CustomActionScopeAndMethod(t *testing.T) {
	md := model.Define("widget", model.LabelPlural("Sales Orders"))
	cases := []struct {
		name         string
		route        ExplicitRoute
		wantMethod   string
		wantPath     string
		wantIDParam  bool
		wantNotFound [2]string
	}{
		{"record default", actionRoute("testmodule.widget", "confirm"), "POST", "/testmodule/sales-orders/{id}/confirm", true, [2]string{"POST", "/testmodule/sales-orders/confirm"}},
		{"record with method", ExplicitRoute{Model: "testmodule.widget", Name: "confirm", Method: "PUT", Auth: "required"}, "PUT", "/testmodule/sales-orders/{id}/confirm", true, [2]string{"POST", "/testmodule/sales-orders/{id}/confirm"}},
		{"collection default method", ExplicitRoute{Model: "testmodule.widget", Name: "bulk_import", Scope: "collection", Auth: "required"}, "POST", "/testmodule/sales-orders/bulk_import", false, [2]string{"POST", "/testmodule/sales-orders/{id}/bulk_import"}},
		{"collection with method", ExplicitRoute{Model: "testmodule.widget", Name: "export", Scope: "collection", Method: "GET", Auth: "required"}, "GET", "/testmodule/sales-orders/export", false, [2]string{"POST", "/testmodule/sales-orders/export"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table := New()
			if _, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{tc.route}, []model.ModelDeclaration{*md}); err != nil {
				t.Fatalf("RegisterRoutes: %v", err)
			}

			entry := lookupAction(t, table, tc.wantMethod, tc.wantPath)
			gotKind, hasID := entry.Manifest.PathParams["id"]
			if hasID != tc.wantIDParam || (hasID && gotKind != "uuid") {
				t.Fatalf("PathParams = %v, want id=uuid present=%v", entry.Manifest.PathParams, tc.wantIDParam)
			}
			if _, _, result, _ := table.Lookup(tc.wantNotFound[0], tc.wantNotFound[1]); result == RouteFound {
				t.Fatalf("%s %s: found a route the action must not expose", tc.wantNotFound[0], tc.wantNotFound[1])
			}
		})
	}
}

func TestRegisterRoutes_ActionKeepsDeclaredOptions(t *testing.T) {
	table := New()
	md := model.Define("widget")
	r := actionRoute("testmodule.widget", "confirm")
	r.Permissions = []string{"testmodule:widget:confirm"}
	r.PathParams = map[string]string{"id": "slug"}

	if _, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{r}, []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	entry := lookupAction(t, table, "POST", "/testmodule/widgets/{id}/confirm")
	if entry.Manifest.Auth != "required" || len(entry.Manifest.Permissions) != 1 {
		t.Fatalf("manifest = %+v, want declared auth and permissions kept", entry.Manifest)
	}
	if entry.Manifest.PathParams["id"] != "slug" {
		t.Fatalf("PathParams = %v, want the declared id kind kept", entry.Manifest.PathParams)
	}
}

func TestRegisterRoutes_ReservedActionHasNoAutoPathParams(t *testing.T) {
	table := New()
	md := model.Define("widget")

	if _, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{actionRoute("testmodule.widget", "get")}, []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if got := lookupAction(t, table, "GET", "/testmodule/widgets/{id}").Manifest.PathParams; len(got) != 0 {
		t.Fatalf("PathParams = %v, want none, matching the EnableOps route it overrides", got)
	}
}

func TestRegisterRoutes_ReservedActionWithMethodFails(t *testing.T) {
	table := New()
	md := model.Define("widget")
	r := actionRoute("testmodule.widget", "list")
	r.Method = "POST"

	_, err := RegisterRoutes(table, "testmodule", "domain", []ExplicitRoute{r}, []model.ModelDeclaration{*md})
	if err == nil {
		t.Fatal("RegisterRoutes succeeded, want an error")
	}
	for _, want := range []string{`"testmodule"`, `"testmodule.widget"`, `"list"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %s", err, want)
		}
	}
}

func TestRegisterRoutes_DuplicateActionIdentityFails(t *testing.T) {
	table := New()
	md := model.Define("widget")
	explicit := []ExplicitRoute{actionRoute("testmodule.widget", "confirm"), actionRoute("testmodule.widget", "confirm")}

	_, err := RegisterRoutes(table, "testmodule", "domain", explicit, []model.ModelDeclaration{*md})
	if err == nil {
		t.Fatal("RegisterRoutes succeeded, want an error")
	}
	for _, want := range []string{`"testmodule"`, `"testmodule.widget"`, `"confirm"`, "more than once"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %s", err, want)
		}
	}
}

func TestRegisterRoutes_InvalidMethodOrScopeFails(t *testing.T) {
	md := model.Define("widget")
	cases := []struct {
		name  string
		route ExplicitRoute
		want  string
	}{
		{"lowercase method", ExplicitRoute{Model: "testmodule.widget", Name: "confirm", Method: "put"}, `unsupported method "put"`},
		{"unknown method", ExplicitRoute{Model: "testmodule.widget", Name: "confirm", Method: "PUTT"}, `unsupported method "PUTT"`},
		{"unknown scope", ExplicitRoute{Model: "testmodule.widget", Name: "confirm", Scope: "collectoin"}, `unknown scope "collectoin"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RegisterRoutes(New(), "testmodule", "domain", []ExplicitRoute{tc.route}, []model.ModelDeclaration{*md})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), `"confirm"`) {
				t.Fatalf("error = %v, want one naming the action and containing %s", err, tc.want)
			}
		})
	}
}

func TestRegisterRoutes_DuplicateActionThroughBothModelNamesFails(t *testing.T) {
	md := model.Define("testmodule.widget")
	explicit := []ExplicitRoute{actionRoute("testmodule.widget", "confirm"), actionRoute("testmodule.testmodule.widget", "confirm")}

	_, err := RegisterRoutes(New(), "testmodule", "domain", explicit, []model.ModelDeclaration{*md})
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("error = %v, want a duplicate-action error", err)
	}
}
