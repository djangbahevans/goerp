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
// carrying Model/Permissions, an EnableOps(List) auto-derived route, a
// manifest View, a manifest NavGroup, a permission, and a public config
// entry — enough to exercise every field dispatchSchemaRoute (goerp#573)
// reflects.
func newSchemaFixtureEngine(t *testing.T) *Engine {
	t.Helper()

	widgetModel := model.Define("widget").WithStandardFields().
		Field("name", model.Text().Required()).
		Field("owner", model.Many2One("widgets.owner")).
		EnableOps(model.List)

	loadedModules := map[string]*module.LoadedModule{
		"widgets": {
			Status: module.StatusReady,
			Manifest: manifest.Manifest{
				Name:        "widgets",
				DisplayName: "Widgets",
				Type:        "standard",
				Version:     "1.3.0",
				Views: []manifest.View{
					{Name: "widgets.list", Type: "list", Resource: "widgets.widget", Label: "Widgets"},
				},
				Navigation: []manifest.NavGroup{
					{Label: "Widgets", Order: 1, Children: []manifest.NavItem{
						{Label: "All Widgets", Route: "/widgets", View: "widgets.list"},
					}},
				},
				Permissions: []manifest.Permission{
					{Name: "widgets:widget:read", Description: "Read widgets"},
				},
				ConfigSchema: []manifest.ConfigEntry{
					{Key: "widgets.feature_flag", Label: "Feature flag", Type: "boolean", Default: true, Public: true},
					{Key: "widgets.internal_secret", Label: "Internal", Type: "string", Default: "x", Public: false},
				},
			},
			ExplicitRoutes: []sdkengine.RouteDeclaration{
				{
					Method:      "GET",
					Path:        "/ping",
					Auth:        "required",
					Permissions: []string{"widgets:widget:read"},
					Model:       "widgets.widget",
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
	if resp.EngineVersion == "" {
		t.Error("engine_version is empty")
	}
	if resp.SchemaHash == "" {
		t.Error("schema_hash is empty")
	}

	mod, ok := resp.Modules["widgets"]
	if !ok {
		t.Fatalf("modules missing \"widgets\", got %v", resp.Modules)
	}
	if mod.Name != "widgets" {
		t.Errorf("name = %q, want %q", mod.Name, "widgets")
	}
	if mod.Version != "1.3.0" {
		t.Errorf("version = %q, want %q", mod.Version, "1.3.0")
	}
	if mod.DisplayName != "Widgets" {
		t.Errorf("display_name = %q, want %q", mod.DisplayName, "Widgets")
	}
	if mod.Frontend != nil {
		t.Errorf("frontend = %+v, want nil (goerp#588 not built yet)", mod.Frontend)
	}

	if len(mod.Views) != 1 || mod.Views[0].Name != "widgets.list" {
		t.Errorf("views = %+v, want one view named widgets.list", mod.Views)
	}
	if len(mod.Navigation) != 1 || mod.Navigation[0].Label != "Widgets" {
		t.Errorf("navigation = %+v, want one group labeled Widgets", mod.Navigation)
	}

	if len(mod.Permissions) != 1 || mod.Permissions[0].Name != "widgets:widget:read" {
		t.Errorf("permissions = %+v, want one permission widgets:widget:read", mod.Permissions)
	}

	if got, want := mod.PublicConfig["widgets.feature_flag"], true; got != want {
		t.Errorf("public_config[widgets.feature_flag] = %v, want %v", got, want)
	}
	if _, ok := mod.PublicConfig["widgets.internal_secret"]; ok {
		t.Error("public_config includes non-public entry widgets.internal_secret")
	}

	md, ok := mod.Models["widgets.widget"]
	if !ok {
		t.Fatalf("models missing \"widgets.widget\", got %v", mod.Models)
	}
	if md.Name != "widget" {
		t.Errorf("model name = %q, want %q", md.Name, "widget")
	}
	if len(md.EnabledOps) != 1 || md.EnabledOps[0] != "list" {
		t.Errorf("model enabled_ops = %v, want [list]", md.EnabledOps)
	}
	var nameField, ownerField *metaSchemaField
	for i, f := range md.Fields {
		switch f.Name {
		case "name":
			nameField = &md.Fields[i]
		case "owner":
			ownerField = &md.Fields[i]
		}
	}
	if nameField == nil || nameField.Type != "text" || !nameField.Required {
		t.Errorf("name field = %+v, want type=text required=true", nameField)
	}
	if ownerField == nil || ownerField.Type != "many2one" || ownerField.RelatedModel != "widgets.owner" {
		t.Errorf("owner field = %+v, want type=many2one related_model=widgets.owner", ownerField)
	}

	var handWritten, enableOpsRoute *metaSchemaRoute
	for i, r := range mod.Routes {
		switch {
		case r.Path == "/widgets/ping":
			handWritten = &mod.Routes[i]
		case r.Model == "widgets.widget" && r.ResponseIsList:
			enableOpsRoute = &mod.Routes[i]
		}
	}

	if handWritten == nil {
		t.Fatalf("no hand-written route at /widgets/ping in %+v", mod.Routes)
	}
	if handWritten.Method != "GET" {
		t.Errorf("hand-written route method = %q, want GET", handWritten.Method)
	}
	if handWritten.Model != "widgets.widget" {
		t.Errorf("hand-written route model = %q, want widgets.widget", handWritten.Model)
	}
	if len(handWritten.Permissions) != 1 || handWritten.Permissions[0] != "widgets:widget:read" {
		t.Errorf("hand-written route permissions = %v, want [widgets:widget:read]", handWritten.Permissions)
	}

	if enableOpsRoute == nil {
		t.Fatalf("no EnableOps-derived list route for widgets.widget in %+v", mod.Routes)
	}
	if enableOpsRoute.Method != "GET" || enableOpsRoute.Path != "/widgets/widgets" {
		t.Errorf("EnableOps route = %+v, want GET /widgets/widgets", enableOpsRoute)
	}
	if enableOpsRoute.CrudAction != "list" {
		t.Errorf("EnableOps route crud_action = %q, want list", enableOpsRoute.CrudAction)
	}
	if enableOpsRoute.View != "widgets.list" {
		t.Errorf("EnableOps route view = %q, want widgets.list", enableOpsRoute.View)
	}
}

func TestDispatchSchemaRoute_RootPathIsExpanded(t *testing.T) {
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
	if len(routes) != 1 || routes[0].Path != "/contacts" {
		t.Fatalf("routes = %+v, want one route with expanded path \"/contacts\"", routes)
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
