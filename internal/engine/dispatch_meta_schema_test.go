package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	sdkengine "github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"go.opentelemetry.io/otel/trace/noop"
)

// newSchemaFixtureEngine builds a *registry.ModuleRegistry-backed *Engine
// with one StatusReady "widgets" module declaring: a hand-written route
// carrying Name/Model/ResponseIsList/CRUDAction/QueryParams (as
// engine.Model/engine.QueryParam would produce), an EnableOps(List)
// auto-derived route, a manifest View, and a manifest NavGroup — enough
// to exercise every field dispatchSchemaRoute (goerp#573) reflects.
func newSchemaFixtureEngine(t *testing.T) *Engine {
	t.Helper()

	widgetModel := model.Define("widget").WithStandardFields().
		Field("name", model.Text().Required()).
		EnableOps(model.List)

	loadedModules := map[string]*module.LoadedModule{
		"widgets": {
			Status: module.StatusReady,
			Manifest: manifest.Manifest{
				Name:    "widgets",
				Type:    "standard",
				Version: "1.3.0",
				Views: []manifest.View{
					{Name: "widgets.list", Type: "list", Resource: "widget", Label: "Widgets"},
				},
				Navigation: []manifest.NavGroup{
					{Label: "Widgets", Order: 1, Children: []manifest.NavItem{
						{Label: "All Widgets", Route: "/widgets", View: "widgets.list"},
					}},
				},
			},
			ExplicitRoutes: []sdkengine.RouteDeclaration{
				{
					Method:         "GET",
					Path:           "/ping",
					Auth:           "required",
					Permissions:    []string{"widgets:widget:read"},
					Name:           "widgets.pingWidgets",
					Model:          "widgets.widget",
					ResponseIsList: false,
					CRUDAction:     "",
					QueryParams: map[string]sdkengine.QueryParamDecl{
						"q": {Type: "string"},
					},
				},
			},
			ModelDecls: []model.ModelDeclaration{*widgetModel},
		},
	}

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(loadedModules); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}

	return &Engine{moduleRegistry: reg}
}

func schemaRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	ctx := withTenantContext(r.Context(), &tenantresolve.TenantContext{
		TenantID: "00000000-0000-0000-0000-000000000001",
		Slug:     "schematest",
	})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})
	return r.WithContext(ctx)
}

func TestDispatchSchemaRoute_ReflectsRoutesViewsAndNavigation(t *testing.T) {
	e := newSchemaFixtureEngine(t)

	w := httptest.NewRecorder()
	e.dispatchSchemaRoute(w, schemaRequest(http.MethodGet, "/_meta/schema"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp metaSchemaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Version == "" {
		t.Error("version is empty, want a timestamp")
	}

	mod, ok := resp.Modules["widgets"]
	if !ok {
		t.Fatalf("modules missing \"widgets\", got %v", resp.Modules)
	}
	if mod.Version != "1.3.0" {
		t.Errorf("modules[widgets].version = %q, want %q", mod.Version, "1.3.0")
	}

	if len(mod.Views) != 1 || mod.Views[0].Name != "widgets.list" {
		t.Errorf("views = %+v, want one view named widgets.list", mod.Views)
	}
	if len(mod.Navigation) != 1 || mod.Navigation[0].Label != "Widgets" {
		t.Errorf("navigation = %+v, want one group labeled Widgets", mod.Navigation)
	}

	var handWritten, enableOpsRoute *metaSchemaRoute
	for i, r := range mod.Routes {
		switch r.Name {
		case "widgets.pingWidgets":
			handWritten = &mod.Routes[i]
		default:
			if r.Model == "widgets.widget" && r.ResponseIsList {
				enableOpsRoute = &mod.Routes[i]
			}
		}
	}

	if handWritten == nil {
		t.Fatalf("no hand-written route named widgets.pingWidgets in %+v", mod.Routes)
	}
	if handWritten.Method != "GET" {
		t.Errorf("hand-written route method = %q, want GET", handWritten.Method)
	}
	if handWritten.Path != "/ping" {
		t.Errorf("hand-written route path = %q, want %q (declared, not expanded)", handWritten.Path, "/ping")
	}
	if handWritten.ExpandedPath != "/widgets/ping" {
		t.Errorf("hand-written route expanded_path = %q, want %q", handWritten.ExpandedPath, "/widgets/ping")
	}
	if handWritten.Model != "widgets.widget" {
		t.Errorf("hand-written route model = %q, want widgets.widget", handWritten.Model)
	}
	if len(handWritten.Permissions) != 1 || handWritten.Permissions[0] != "widgets:widget:read" {
		t.Errorf("hand-written route permissions = %v, want [widgets:widget:read]", handWritten.Permissions)
	}
	if got := handWritten.QueryParams["q"].Type; got != "string" {
		t.Errorf(`hand-written route query_params["q"].type = %q, want "string"`, got)
	}

	if enableOpsRoute == nil {
		t.Fatalf("no EnableOps-derived list route for widgets.widget in %+v", mod.Routes)
	}
	if enableOpsRoute.Method != "GET" || enableOpsRoute.ExpandedPath != "/widgets/widgets" {
		t.Errorf("EnableOps route = %+v, want GET /widgets/widgets", enableOpsRoute)
	}
	if enableOpsRoute.Path != "/widgets" {
		t.Errorf("EnableOps route declared path = %q, want %q (expanded path with module prefix stripped)", enableOpsRoute.Path, "/widgets")
	}
}

func TestDispatchSchemaRoute_RootPathReportsSlash(t *testing.T) {
	loadedModules := map[string]*module.LoadedModule{
		"contacts": {
			Status:         module.StatusReady,
			Manifest:       manifest.Manifest{Name: "contacts", Type: "standard", Version: "1.0.0"},
			ExplicitRoutes: []sdkengine.RouteDeclaration{{Method: "GET", Path: "/", Auth: "required"}},
		},
	}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(loadedModules); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	e := &Engine{moduleRegistry: reg}

	w := httptest.NewRecorder()
	e.dispatchSchemaRoute(w, schemaRequest(http.MethodGet, "/_meta/schema"))

	var resp metaSchemaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	routes := resp.Modules["contacts"].Routes
	if len(routes) != 1 || routes[0].Path != "/" {
		t.Fatalf("routes = %+v, want one route with path \"/\"", routes)
	}
	if routes[0].ExpandedPath != "/contacts" {
		t.Errorf("expanded_path = %q, want %q", routes[0].ExpandedPath, "/contacts")
	}
}

func TestDispatchSchemaRoute_ExcludesFailedModules(t *testing.T) {
	loadedModules := map[string]*module.LoadedModule{
		"broken": {
			Status:        module.StatusFailed,
			Manifest:      manifest.Manifest{Name: "broken", Type: "standard"},
			FailureReason: "compile error",
		},
	}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(loadedModules); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	e := &Engine{moduleRegistry: reg}

	w := httptest.NewRecorder()
	e.dispatchSchemaRoute(w, schemaRequest(http.MethodGet, "/_meta/schema"))

	var resp metaSchemaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp.Modules["broken"]; ok {
		t.Errorf("modules = %v, want failed module \"broken\" excluded", resp.Modules)
	}
}

// TestDispatchSchemaRoute_NoTokenReturns401 is the same regression guard
// TestDispatchPermissionsRoute_NoTokenReturns401 is for /_meta/permissions
// — a handler-level unit test can't catch a missing Auth: "required" on
// this route's registration, only a real request through the full
// middleware chain can.
func TestDispatchSchemaRoute_NoTokenReturns401(t *testing.T) {
	f := newChainFixture(t)

	generousDefault := route.RateLimitConfig{Requests: 10000, WindowSeconds: 60, Scope: "ip"}
	e := &Engine{moduleRegistry: f.reg}
	builtins := map[string]http.Handler{
		"GET /_meta/schema": http.HandlerFunc(e.dispatchSchemaRoute),
	}
	h := buildChain(e, f.reg, builtins, nil, f.resolver, f.checker, noop.NewTracerProvider().Tracer("test"), f.cacheClient, generousDefault)

	req := httptest.NewRequest(http.MethodGet, "/_meta/schema", nil)
	req.Host = f.domain
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "unauthenticated" {
		t.Errorf("error.code = %q, want %q", code, "unauthenticated")
	}
}
