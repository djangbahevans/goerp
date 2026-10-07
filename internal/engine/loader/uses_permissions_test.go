package loader

import (
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func ownerModule() *module.LoadedModule {
	return loadedModule(manifest.Manifest{
		Name:        "contacts",
		Permissions: []manifest.Permission{{Name: "contacts:contact:read", Description: "Read"}},
	})
}

func TestValidateUsesPermissions_Owners(t *testing.T) {
	tests := []struct {
		name    string
		depends []string
		soft    []string
		owner   func() *module.LoadedModule
		uses    string
		wantErr string
	}{
		{"hard dependency declaring it", []string{"contacts"}, nil, ownerModule, "contacts:contact:read", ""},
		{"soft dependency declaring it", nil, []string{"contacts"}, ownerModule, "contacts:contact:read", ""},
		{"owner not a dependency", nil, nil, ownerModule, "contacts:contact:read", "in neither depends_on nor soft_depends_on"},
		{"owner lacks the permission", []string{"contacts"}, nil, ownerModule, "contacts:contact:write", `module "contacts" declares no permission "contacts:contact:write"`},
		{"hard owner not loaded", []string{"contacts"}, nil, func() *module.LoadedModule { return nil }, "contacts:contact:read", "which is not loaded"},
		{"soft owner not loaded", nil, []string{"contacts"}, func() *module.LoadedModule { return nil }, "contacts:contact:read", ""},
		{"soft owner failed", nil, []string{"contacts"}, func() *module.LoadedModule {
			m := ownerModule()
			m.Fail("broken")
			return m
		}, "contacts:contact:read", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := loadedModule(manifest.Manifest{
				Name: "sales", DependsOn: tc.depends, SoftDependsOn: tc.soft, UsesPermissions: []string{tc.uses},
			})
			modules := map[string]*module.LoadedModule{"sales": reader}
			if o := tc.owner(); o != nil {
				modules["contacts"] = o
			}

			ValidateUsesPermissions(modules)

			failed := reader.Status == module.StatusFailed
			switch {
			case tc.wantErr == "" && failed:
				t.Fatalf("reader failed: %s", reader.FailureReason)
			case tc.wantErr != "" && (!failed || !strings.Contains(reader.FailureReason, tc.wantErr)):
				t.Fatalf("status = %v, reason = %q, want a failure containing %q", reader.Status, reader.FailureReason, tc.wantErr)
			}
		})
	}
}

func TestValidateUsesPermissions_References(t *testing.T) {
	declared := manifest.Manifest{
		Name:            "sales",
		DependsOn:       []string{"contacts"},
		Permissions:     []manifest.Permission{{Name: "sales:order:read", Description: "Read"}},
		UsesPermissions: []string{"contacts:contact:read"},
	}
	selection := func(f model.FieldDef) []model.ModelDeclaration {
		return []model.ModelDeclaration{{Name: "sales.order", Fields: []model.NamedField{{Name: "state", Def: f}}}}
	}
	workflow := model.Selection("draft", "confirmed").Workflow(model.Transition("draft", "confirmed", "confirm").Requires(perm.Ref("sales:order:confirm")))

	tests := []struct {
		name    string
		edit    func(*module.LoadedModule)
		wantErr string
	}{
		{"declared and used permissions resolve", func(m *module.LoadedModule) {
			m.ExplicitRoutes = []abiv1.RouteDeclaration{{Method: "GET", Path: "/orders", Permissions: []string{"sales:order:read", "contacts:contact:read"}}}
			m.Manifest.Views = []manifest.View{{Name: "order_list", Permission: "sales:order:read"}}
			m.Manifest.Reports = []manifest.Report{{Name: "summary", Permissions: []string{"contacts:contact:read"}}}
		}, ""},
		{"route", func(m *module.LoadedModule) {
			m.ExplicitRoutes = []abiv1.RouteDeclaration{{Method: "POST", Path: "/orders", Permissions: []string{"sales:order:write"}}}
		}, `route POST /orders names permission "sales:order:write"`},
		{"action", func(m *module.LoadedModule) {
			m.ExplicitRoutes = []abiv1.RouteDeclaration{{Method: "POST", Path: "/orders/{id}/ship", Name: "ship", Permissions: []string{"sales:order:ship"}}}
		}, `action "ship"`},
		{"workflow transition", func(m *module.LoadedModule) { m.ModelDecls = selection(workflow) },
			`model sales.order field state workflow transition "confirm" names permission "sales:order:confirm"`},
		{"field read access", func(m *module.LoadedModule) {
			m.ModelDecls = selection(model.Text().Access(model.AccessRead(perm.Ref("sales:order:secret"))))
		}, `field state read access names permission "sales:order:secret"`},
		{"field write access", func(m *module.LoadedModule) {
			m.ModelDecls = selection(model.Text().Access(model.AccessWrite(perm.Ref("sales:order:secret"))))
		}, `field state write access names permission "sales:order:secret"`},
		{"view", func(m *module.LoadedModule) {
			m.Manifest.Views = []manifest.View{{Name: "order_list", Permission: "sales:order:typo"}}
		}, `view "order_list": permission names permission "sales:order:typo"`},
		{"navigation item", func(m *module.LoadedModule) {
			m.Manifest.Navigation = []manifest.NavGroup{{Label: "Sales", Children: []manifest.NavItem{{Label: "Orders", Permission: "sales:order:gone"}}}}
		}, `navigation group "Sales": children[0].permission names permission "sales:order:gone"`},
		{"report", func(m *module.LoadedModule) {
			m.Manifest.Reports = []manifest.Report{{Name: "summary", Permissions: []string{"sales:order:read", "sales:order:nope"}}}
		}, `report "summary": permissions[1] names permission "sales:order:nope"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := loadedModule(declared)
			tc.edit(reader)
			modules := map[string]*module.LoadedModule{"sales": reader, "contacts": ownerModule()}

			ValidateUsesPermissions(modules)

			failed := reader.Status == module.StatusFailed
			switch {
			case tc.wantErr == "" && failed:
				t.Fatalf("reader failed: %s", reader.FailureReason)
			case tc.wantErr != "" && (!failed || !strings.Contains(reader.FailureReason, tc.wantErr)):
				t.Fatalf("status = %v, reason = %q, want a failure containing %q", reader.Status, reader.FailureReason, tc.wantErr)
			}
		})
	}
}

// A module this pass fails must not change the outcome for its readers,
// whichever order the map yields them in.
func TestValidateUsesPermissions_OutcomeIndependentOfVisitOrder(t *testing.T) {
	for range 50 {
		owner := ownerModule()
		owner.Manifest.Views = []manifest.View{{Name: "v", Permission: "contacts:contact:undeclared"}}
		reader := loadedModule(manifest.Manifest{Name: "sales", DependsOn: []string{"contacts"}, UsesPermissions: []string{"contacts:contact:read"}})
		modules := map[string]*module.LoadedModule{"contacts": owner, "sales": reader}

		ValidateUsesPermissions(modules)

		if owner.Status != module.StatusFailed {
			t.Fatal("owner with an undeclared view permission did not fail")
		}
		if reader.Status == module.StatusFailed {
			t.Fatalf("reader failed because its owner failed: %s", reader.FailureReason)
		}
	}
}

func TestLoadAll_UsesPermissions_PolicyScopesADependencyPermission(t *testing.T) {
	owner := Source{
		Name: "crm",
		ManifestBytes: manifestJSONWithFields(t, "crm", okModule, []string{"db.read"}, map[string]any{
			"permissions": []map[string]any{{"name": "crm:contact:read", "description": "Read contacts"}},
		}),
		WasmBytes: okModule,
	}
	scoped := func(fields map[string]any) Source {
		return Source{
			Name:          "sales",
			ManifestBytes: manifestJSONWithFields(t, "sales", okModule, []string{"db.read"}, fields),
			WasmBytes:     okModule,
		}
	}
	policy := []map[string]any{{"name": "sales:contact:own_only", "applies_to": "crm:contact:read", "condition": "record.owner_id = current_user.id"}}

	loaded := LoadAll(t.Context(), newTestRuntime(t), testPoolCfg(), []Source{owner, scoped(map[string]any{
		"depends_on": []string{"crm"}, "uses_permissions": []string{"crm:contact:read"}, "policies": policy,
	})})
	if m := loaded["sales"]; m.Status == module.StatusFailed {
		t.Fatalf("sales failed: %s", m.FailureReason)
	}

	loaded = LoadAll(t.Context(), newTestRuntime(t), testPoolCfg(), []Source{owner, scoped(map[string]any{
		"uses_permissions": []string{"crm:contact:read"}, "policies": policy,
	})})
	if m := loaded["sales"]; m.Status != module.StatusFailed || !strings.Contains(m.FailureReason, "in neither depends_on nor soft_depends_on") {
		t.Fatalf("sales status = %v, reason = %q, want a failure on the missing dependency", m.Status, m.FailureReason)
	}
}

func TestLoadAll_ViewNamingAnUndeclaredPermissionFailsLoad(t *testing.T) {
	src := Source{
		Name: "sales",
		ManifestBytes: manifestJSONWithFields(t, "sales", okModule, []string{"db.read"}, map[string]any{
			"views": []map[string]any{{"name": "order_list", "type": "list", "resource": "sales.order", "label": "Orders", "permission": "sales:order:read"}},
		}),
		WasmBytes: okModule,
	}

	loaded := LoadAll(t.Context(), newTestRuntime(t), testPoolCfg(), []Source{src})

	m := loaded["sales"]
	if m.Status != module.StatusFailed || !strings.Contains(m.FailureReason, `view "order_list"`) || !strings.Contains(m.FailureReason, `"sales:order:read"`) {
		t.Fatalf("status = %v, reason = %q, want a failure naming the view and permission", m.Status, m.FailureReason)
	}
}
