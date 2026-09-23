package engine

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
		Field("name", model.Text().Required().Primary()).
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
				{
					Auth:  "required",
					Model: "widgets.widget",
					Name:  "confirm",
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

	var handWritten, enableOpsRoute, customAction *metaSchemaRoute
	for i, r := range mod.Routes {
		switch {
		case r.Path == "/widgets/ping":
			handWritten = &mod.Routes[i]
		case r.Model == "widgets.widget" && r.ResponseIsList:
			enableOpsRoute = &mod.Routes[i]
		case r.Name == "confirm":
			customAction = &mod.Routes[i]
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

	if customAction == nil {
		t.Fatalf("no custom action route named confirm in %+v", mod.Routes)
	}
	if customAction.Method != "POST" || customAction.Path != "/widgets/widgets/{id}/confirm" {
		t.Errorf("custom action route = %+v, want POST /widgets/widgets/{id}/confirm", customAction)
	}
	if customAction.CrudAction != "" {
		t.Errorf("custom action route crud_action = %q, want empty", customAction.CrudAction)
	}
}

func TestDispatchSchemaRoute_ReflectsViewExtensionsAndLoadOrder(t *testing.T) {
	loadedModules := map[string]*module.LoadedModule{
		"contacts": {
			Status:    module.StatusReady,
			LoadOrder: 0,
			Manifest:  manifest.Manifest{Name: "contacts", DisplayName: "Contacts", Type: "standard", Version: "1.0.0"},
		},
		"hr": {
			Status:    module.StatusReady,
			LoadOrder: 1,
			Manifest: manifest.Manifest{
				Name: "hr", DisplayName: "HR", Type: "standard", Version: "1.0.0",
				ViewExtensions: []manifest.ViewExtensionRef{
					{Extends: "contacts.contacts_form", Extension: "hr_employees_tab"},
				},
				ViewExtensionDefinitions: []manifest.ViewExtensionDef{
					{
						Name: "hr_employees_tab", Type: "tab", TargetSection: "tabs", Position: "append",
						Tab: &manifest.FormTab{Label: "Employees", Type: "view", View: "hr.employees_list"},
					},
				},
			},
		},
	}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(loadedModules); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	e := &Engine{moduleRegistry: reg}

	w := httptest.NewRecorder()
	e.dispatchSchemaRoute(w, schemaRequest(http.MethodGet, "/_meta/schema"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp metaSchemaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	contacts, ok := resp.Modules["contacts"]
	if !ok {
		t.Fatalf("modules missing \"contacts\"")
	}
	if contacts.LoadOrder != 0 {
		t.Errorf("contacts.LoadOrder = %d, want 0", contacts.LoadOrder)
	}
	if contacts.ViewExtensions == nil || len(contacts.ViewExtensions) != 0 {
		t.Errorf("contacts.ViewExtensions = %v, want non-nil empty slice", contacts.ViewExtensions)
	}
	if contacts.ViewExtensionDefinitions == nil || len(contacts.ViewExtensionDefinitions) != 0 {
		t.Errorf("contacts.ViewExtensionDefinitions = %v, want non-nil empty slice", contacts.ViewExtensionDefinitions)
	}

	hr, ok := resp.Modules["hr"]
	if !ok {
		t.Fatalf("modules missing \"hr\"")
	}
	if hr.LoadOrder != 1 {
		t.Errorf("hr.LoadOrder = %d, want 1", hr.LoadOrder)
	}
	if len(hr.ViewExtensions) != 1 || hr.ViewExtensions[0].Extends != "contacts.contacts_form" {
		t.Errorf("hr.ViewExtensions = %+v, want one ref extending contacts.contacts_form", hr.ViewExtensions)
	}
	if len(hr.ViewExtensionDefinitions) != 1 || hr.ViewExtensionDefinitions[0].Tab == nil || hr.ViewExtensionDefinitions[0].Tab.View != "hr.employees_list" {
		t.Errorf("hr.ViewExtensionDefinitions = %+v, want one tab def targeting hr.employees_list", hr.ViewExtensionDefinitions)
	}
}

func TestMetaSchemaViewFor(t *testing.T) {
	views := []manifest.View{
		{Name: "widgets.kanban", Type: "kanban", Resource: "widgets.widget"},
		{Name: "widgets.list", Type: "list", Resource: "widgets.widget"},
		{Name: "widgets.form", Type: "form", Resource: "widgets.widget"},
		{Name: "other.list", Type: "list", Resource: "other.thing"},
	}

	cases := []struct {
		crudAction string
		want       string
	}{
		{"list", "widgets.kanban"}, // first list-shaped match wins by declaration order
		{"get", "widgets.form"},
		{"create", "widgets.form"},
		{"update", "widgets.form"},
		{"delete", ""}, // no view claims delete
	}
	for _, c := range cases {
		if got := metaSchemaViewFor(views, "widgets.widget", c.crudAction); got != c.want {
			t.Errorf("metaSchemaViewFor(%q) = %q, want %q", c.crudAction, got, c.want)
		}
	}

	if got := metaSchemaViewFor(views, "unknown.model", "list"); got != "" {
		t.Errorf("metaSchemaViewFor for unknown model = %q, want \"\"", got)
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

func TestDispatchSchemaRoute_OmitsZeroValueBooleanAndNumericMembers(t *testing.T) {
	e := newSchemaFixtureEngine(t)

	w := httptest.NewRecorder()
	e.dispatchSchemaRoute(w, schemaRequest(http.MethodGet, "/_meta/schema"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var body struct {
		Modules map[string]struct {
			Views      []map[string]any `json:"views"`
			Navigation []struct {
				Children []map[string]any `json:"children"`
			} `json:"navigation"`
			Models map[string]struct {
				Fields []map[string]any `json:"fields"`
			} `json:"models"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	mod := body.Modules["widgets"]

	for _, member := range []string{"selectable", "default_page_size", "chatter", "autosave"} {
		if _, ok := mod.Views[0][member]; ok {
			t.Errorf("view serves %q, want it omitted; view: %v", member, mod.Views[0])
		}
	}
	if _, ok := mod.Navigation[0].Children[0]["external"]; ok {
		t.Errorf("navigation item serves \"external\", want it omitted")
	}

	fields := map[string]map[string]any{}
	for _, f := range mod.Models["widgets.widget"].Fields {
		fields[f["name"].(string)] = f
	}
	if fields["name"]["required"] != true {
		t.Errorf("required field serves required = %v, want true", fields["name"]["required"])
	}
	if _, ok := fields["owner"]["required"]; ok {
		t.Errorf("optional field serves \"required\", want it omitted")
	}
	if fields["name"]["is_primary"] != true {
		t.Errorf("Primary() field serves is_primary = %v, want true", fields["name"]["is_primary"])
	}
	if _, ok := fields["owner"]["is_primary"]; ok {
		t.Errorf("non-Primary() field serves \"is_primary\", want it omitted")
	}
}

func TestDispatchSchemaRoute_NilSlicesEncodeAsEmptyArrays(t *testing.T) {
	bareModel := model.Define("gadget").WithStandardFields().EnableOps(model.List)
	reg := &registry.ModuleRegistry{}
	_, err := reg.Update(map[string]*module.LoadedModule{
		"bare": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Name: "bare", DisplayName: "Bare", Type: "standard", Version: "1.0.0"},
			ModelDecls: []model.ModelDeclaration{*bareModel},
		},
	})
	if err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	e := &Engine{moduleRegistry: reg}

	w := httptest.NewRecorder()
	e.dispatchSchemaRoute(w, schemaRequest(http.MethodGet, "/_meta/schema"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var body struct {
		Modules map[string]map[string]any `json:"modules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	mod := body.Modules["bare"]

	for _, member := range []string{"views", "navigation", "permissions", "routes", "view_extensions", "view_extension_definitions"} {
		if _, ok := mod[member].([]any); !ok {
			t.Errorf("%s = %#v, want a JSON array", member, mod[member])
		}
	}

	routes, _ := mod["routes"].([]any)
	if len(routes) == 0 {
		t.Fatal("routes is empty, want the EnableOps-derived route")
	}
	for _, r := range routes {
		route := r.(map[string]any)
		if _, ok := route["permissions"].([]any); !ok {
			t.Errorf("route %v permissions = %#v, want a JSON array", route["path"], route["permissions"])
		}
	}

	models, _ := mod["models"].(map[string]any)
	for name, m := range models {
		md := m.(map[string]any)
		for _, member := range []string{"fields", "enabled_ops"} {
			if _, ok := md[member].([]any); !ok {
				t.Errorf("model %s %s = %#v, want a JSON array", name, member, md[member])
			}
		}
	}
}

func TestDispatchSchemaRoute_ServesManifestViewsUnchanged(t *testing.T) {
	paths, err := filepath.Glob("../../testdata/manifest-views/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no view fixtures (err: %v)", err)
	}

	var views []manifest.View
	declared := map[string]map[string]any{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var view manifest.View
		if err := json.Unmarshal(data, &view); err != nil {
			t.Fatalf("decode %s: %v", p, err)
		}
		var members map[string]any
		if err := json.Unmarshal(data, &members); err != nil {
			t.Fatalf("decode %s: %v", p, err)
		}
		views = append(views, view)
		declared[view.Name] = members
	}

	reg := &registry.ModuleRegistry{}
	_, err = reg.Update(map[string]*module.LoadedModule{
		"fixtures": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Name: "fixtures", DisplayName: "Fixtures", Type: "standard", Version: "1.0.0", Views: views},
		},
	})
	if err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	e := &Engine{moduleRegistry: reg}

	w := httptest.NewRecorder()
	e.dispatchSchemaRoute(w, schemaRequest(http.MethodGet, "/_meta/schema"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var body struct {
		Modules map[string]struct {
			Views []map[string]any `json:"views"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	served := body.Modules["fixtures"].Views
	if len(served) != len(declared) {
		t.Fatalf("served %d views, want %d", len(served), len(declared))
	}
	for _, got := range served {
		name, _ := got["name"].(string)
		want, ok := declared[name]
		if !ok {
			t.Errorf("served unexpected view %q", name)
			continue
		}
		for _, mismatch := range servedMismatches(name, want, got) {
			t.Error(mismatch)
		}
	}
}

// servedMismatches lists where got differs from want, treating a declared
// value that isEmptyJSONValue as optional: a modeled member's omitempty or
// omitzero tag drops it from the served JSON.
func servedMismatches(path string, want, got any) []string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s = %v, want an object", path, got)}
		}
		var out []string
		for member, wantValue := range w {
			gotValue, present := g[member]
			if !present {
				if !isEmptyJSONValue(wantValue) {
					out = append(out, fmt.Sprintf("%s.%s (%v) is missing from the served view", path, member, wantValue))
				}
				continue
			}
			out = append(out, servedMismatches(path+"."+member, wantValue, gotValue)...)
		}
		for member := range g {
			if _, ok := w[member]; !ok {
				out = append(out, fmt.Sprintf("%s.%s is served but not declared", path, member))
			}
		}
		return out
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return []string{fmt.Sprintf("%s = %v, want %v", path, got, want)}
		}
		var out []string
		for i := range w {
			out = append(out, servedMismatches(fmt.Sprintf("%s[%d]", path, i), w[i], g[i])...)
		}
		return out
	}
	if !reflect.DeepEqual(want, got) {
		return []string{fmt.Sprintf("%s = %v, want %v", path, got, want)}
	}
	return nil
}

// isEmptyJSONValue reports whether v is a null, false, empty array or empty
// object — the values a modeled member's omitempty/omitzero tag drops from
// the served view.
func isEmptyJSONValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}
