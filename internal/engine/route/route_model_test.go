package route

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestRegisterModelRoutes_DerivesOnlyEnabledOps(t *testing.T) {
	table := New()
	md := model.Define("widget").EnableOps(model.List, model.Create)

	suppressed, err := RegisterModelRoutes(table, "testmodule", "domain", []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatalf("RegisterModelRoutes: %v", err)
	}
	if len(suppressed) != 0 {
		t.Fatalf("suppressed = %v, want none", suppressed)
	}

	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/testmodule/widgets"},
		{"POST", "/testmodule/widgets"},
	} {
		entry, _, result, _ := table.Lookup(tc.method, tc.path)
		if result != RouteFound {
			t.Fatalf("%s %s: result = %v, want RouteFound", tc.method, tc.path, result)
		}
		if entry.Manifest.Model != "testmodule.widget" {
			t.Fatalf("%s %s: Model = %q, want %q", tc.method, tc.path, entry.Manifest.Model, "testmodule.widget")
		}
		if !entry.Manifest.EngineNative {
			t.Fatalf("%s %s: EngineNative = false, want true", tc.method, tc.path)
		}
		if entry.Manifest.EngineBuiltin {
			// goerp#369: EngineNative marks dispatch-routing (this route
			// dispatches through dispatchORMRoute, not WASM) — it must
			// never imply EngineBuiltin (tenant/auth middleware bypass),
			// which is reserved for the fixed set of engine infra routes
			// registerBuiltinRoutes registers. An EnableOps-derived route
			// still needs full tenant/auth/permission enforcement.
			t.Fatalf("%s %s: EngineBuiltin = true, want false — an EnableOps route must not bypass tenant/auth middleware", tc.method, tc.path)
		}
		if entry.Manifest.StorageBackend != "table" {
			t.Fatalf("%s %s: StorageBackend = %q, want %q", tc.method, tc.path, entry.Manifest.StorageBackend, "table")
		}
	}

	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/testmodule/widgets/{id}"},
		{"PUT", "/testmodule/widgets/{id}"},
		{"DELETE", "/testmodule/widgets/{id}"},
		{"POST", "/testmodule/widgets/preview"},
	} {
		if _, _, result, _ := table.Lookup(tc.method, tc.path); result == RouteFound {
			t.Fatalf("%s %s: found a route for an op not in EnableOps", tc.method, tc.path)
		}
	}
}

func TestRegisterModelRoutes_CrudActionAndResponseIsList(t *testing.T) {
	table := New()
	md := model.Define("widget").EnableOps(model.List, model.Get, model.Create, model.Update, model.Delete, model.Preview)
	if _, err := RegisterModelRoutes(table, "testmodule", "domain", []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterModelRoutes: %v", err)
	}

	cases := []struct {
		method, path, wantCrudAction string
		wantResponseIsList           bool
	}{
		{"GET", "/testmodule/widgets", "list", true},
		{"GET", "/testmodule/widgets/{id}", "get", false},
		{"POST", "/testmodule/widgets", "create", false},
		{"PUT", "/testmodule/widgets/{id}", "update", false},
		{"DELETE", "/testmodule/widgets/{id}", "delete", false},
		{"POST", "/testmodule/widgets/preview", "preview", false},
	}
	for _, tc := range cases {
		entry, _, result, _ := table.Lookup(tc.method, tc.path)
		if result != RouteFound {
			t.Fatalf("%s %s: result = %v, want RouteFound", tc.method, tc.path, result)
		}
		if entry.Manifest.CrudAction != tc.wantCrudAction {
			t.Fatalf("%s %s: CrudAction = %q, want %q", tc.method, tc.path, entry.Manifest.CrudAction, tc.wantCrudAction)
		}
		if entry.Manifest.ResponseIsList != tc.wantResponseIsList {
			t.Fatalf("%s %s: ResponseIsList = %v, want %v", tc.method, tc.path, entry.Manifest.ResponseIsList, tc.wantResponseIsList)
		}
	}
}

func TestRegisterModelRoutes_NoEnableOpsProducesZeroRoutes(t *testing.T) {
	table := New()
	md := model.Define("widget")
	if _, err := RegisterModelRoutes(table, "testmodule", "domain", []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterModelRoutes: %v", err)
	}

	if _, _, result, _ := table.Lookup("GET", "/testmodule/widgets"); result == RouteFound {
		t.Fatal("found a route for a model with no EnableOps call")
	}
}

func TestRegisterModelRoutes_ExplicitRouteSuppressesAutoDerived(t *testing.T) {
	table := New()
	if err := RegisterModuleRoutes(table, "testmodule", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/widgets"},
	}); err != nil {
		t.Fatalf("RegisterModuleRoutes: %v", err)
	}

	md := model.Define("widget").EnableOps(model.List, model.Create)
	suppressed, err := RegisterModelRoutes(table, "testmodule", "domain", []model.ModelDeclaration{*md})
	if err != nil {
		t.Fatalf("RegisterModelRoutes: %v", err)
	}

	if len(suppressed) != 1 || suppressed[0] != (SuppressedRoute{Model: "testmodule.widget", Op: "list"}) {
		t.Fatalf("suppressed = %v, want exactly [{testmodule.widget list}]", suppressed)
	}

	// The explicit route's own entry (no Model/CrudAction/EngineNative)
	// must still be the one registered — proof the auto-derived
	// candidate didn't overwrite it.
	entry, _, result, _ := table.Lookup("GET", "/testmodule/widgets")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if entry.Manifest.EngineNative {
		t.Fatal("explicit route was overwritten by the auto-derived candidate")
	}

	// Create wasn't claimed explicitly, so it still gets auto-derived.
	entry, _, result, _ = table.Lookup("POST", "/testmodule/widgets")
	if result != RouteFound {
		t.Fatalf("POST result = %v, want RouteFound", result)
	}
	if !entry.Manifest.EngineNative {
		t.Fatal("Create should still be auto-derived since nothing explicit claimed it")
	}
}

func TestRegisterModelRoutes_LabelPluralOverridesModelName(t *testing.T) {
	table := New()
	md := model.Define("widget", model.LabelPlural("Sales Orders")).EnableOps(model.List)
	if _, err := RegisterModelRoutes(table, "testmodule", "domain", []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterModelRoutes: %v", err)
	}

	if _, _, result, _ := table.Lookup("GET", "/testmodule/sales-orders"); result != RouteFound {
		t.Fatalf("result = %v, want RouteFound at /testmodule/sales-orders", result)
	}
}

func TestRegisterModelRoutes_RoutePrefixOverridesDerivedPlural(t *testing.T) {
	table := New()
	md := model.Define("widget").RoutePrefix("/gadgets").EnableOps(model.List)
	if _, err := RegisterModelRoutes(table, "testmodule", "domain", []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterModelRoutes: %v", err)
	}

	if _, _, result, _ := table.Lookup("GET", "/testmodule/gadgets"); result != RouteFound {
		t.Fatalf("result = %v, want RouteFound at /testmodule/gadgets", result)
	}
	if _, _, result, _ := table.Lookup("GET", "/testmodule/widgets"); result == RouteFound {
		t.Fatal("found a route at the derived (non-overridden) plural path")
	}
}

func TestRegisterModelRoutes_StorageBackendMapping(t *testing.T) {
	cases := []struct {
		name    string
		backend model.ModelBackend
		want    string
	}{
		{"table (zero value)", "", "table"},
		{"transient", model.BackendTransient, "transient"},
		{"virtual", model.BackendVirtual, "virtual"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := storageBackendString(tc.backend); got != tc.want {
				t.Fatalf("storageBackendString(%q) = %q, want %q", tc.backend, got, tc.want)
			}
		})
	}
}

func TestRegisterModelRoutes_TwoModelsSameDerivedPathErrors(t *testing.T) {
	table := New()
	md1 := model.Define("widget", model.LabelPlural("Items")).EnableOps(model.List)
	md2 := model.Define("gadget", model.LabelPlural("Items")).EnableOps(model.List)

	if _, err := RegisterModelRoutes(table, "testmodule", "domain", []model.ModelDeclaration{*md1, *md2}); err == nil {
		t.Fatal("RegisterModelRoutes: want an error when two models derive the same route, got nil")
	}
}

func TestRegisterModelRoutes_ConnectorExpansion(t *testing.T) {
	table := New()
	md := model.Define("widget").EnableOps(model.List)
	if _, err := RegisterModelRoutes(table, "connector_paystack", "connector", []model.ModelDeclaration{*md}); err != nil {
		t.Fatalf("RegisterModelRoutes: %v", err)
	}

	if _, _, result, _ := table.Lookup("GET", "/connectors/connector_paystack/widgets"); result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
}
