package registry

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestRegistrySnapshot_ModelByName_ResolvesModuleAndModel(t *testing.T) {
	r := &ModuleRegistry{}
	widget := *model.Define("widget")
	modules := map[string]*module.LoadedModule{
		"testmodule": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Type: "standard"},
			ModelDecls: []model.ModelDeclaration{widget},
		},
	}

	snap, err := r.Update(modules)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	moduleName, mod, md, ok := snap.ModelByName("testmodule.widget")
	if !ok {
		t.Fatal("ModelByName(\"testmodule.widget\") ok = false, want true")
	}
	if moduleName != "testmodule" {
		t.Errorf("moduleName = %q, want %q", moduleName, "testmodule")
	}
	if mod != modules["testmodule"] {
		t.Errorf("mod = %p, want %p", mod, modules["testmodule"])
	}
	if md.Name != "widget" {
		t.Errorf("md.Name = %q, want %q", md.Name, "widget")
	}
}

func TestRegistrySnapshot_ModelByName_UnknownModule(t *testing.T) {
	r := &ModuleRegistry{}
	snap, err := r.Update(map[string]*module.LoadedModule{
		"testmodule": {Status: module.StatusReady, Manifest: manifest.Manifest{Type: "standard"}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, _, _, ok := snap.ModelByName("othermodule.widget"); ok {
		t.Fatal("ModelByName for an unregistered module: ok = true, want false")
	}
}

func TestRegistrySnapshot_ModelByName_UnknownModel(t *testing.T) {
	r := &ModuleRegistry{}
	snap, err := r.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Type: "standard"},
			ModelDecls: []model.ModelDeclaration{*model.Define("widget")},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, _, _, ok := snap.ModelByName("testmodule.gadget"); ok {
		t.Fatal("ModelByName for an undeclared model: ok = true, want false")
	}
}

func TestRegistrySnapshot_ModelByName_SkipsFailedModule(t *testing.T) {
	r := &ModuleRegistry{}
	snap, err := r.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:     module.StatusFailed,
			Manifest:   manifest.Manifest{Type: "standard"},
			ModelDecls: []model.ModelDeclaration{*model.Define("widget")},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, _, _, ok := snap.ModelByName("testmodule.widget"); ok {
		t.Fatal("ModelByName for a StatusFailed module: ok = true, want false")
	}
}

func TestRegistrySnapshot_ModelByName_UnqualifiedNameRejected(t *testing.T) {
	r := &ModuleRegistry{}
	snap, err := r.Update(map[string]*module.LoadedModule{})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, _, _, ok := snap.ModelByName("widget"); ok {
		t.Fatal("ModelByName with no \".\" separator: ok = true, want false")
	}
}

func TestRegistrySnapshot_ModelByName_ResolvesEitherDeclarationSpelling(t *testing.T) {
	for _, declared := range []string{"widget", "testmodule.widget"} {
		t.Run(declared, func(t *testing.T) {
			r := &ModuleRegistry{}
			snap, err := r.Update(map[string]*module.LoadedModule{
				"testmodule": {
					Status:     module.StatusReady,
					Manifest:   manifest.Manifest{Type: "standard"},
					ModelDecls: []model.ModelDeclaration{*model.Define(declared)},
				},
			})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			if _, _, md, ok := snap.ModelByName("testmodule.widget"); !ok || md.Name != declared {
				t.Fatalf("ModelByName(testmodule.widget) = %q, %v, want the %q declaration", md.Name, ok, declared)
			}
			if _, _, _, ok := snap.ModelByName("testmodule.testmodule.widget"); ok {
				t.Fatal("ModelByName resolved a double-qualified name")
			}
		})
	}
}

func TestComputeTargets_CarriesModuleDeclarations(t *testing.T) {
	r := &ModuleRegistry{}
	jobTypes := []manifest.JobType{{Name: "contacts_import", Queue: "bulk"}}
	configSchema := []manifest.ConfigEntry{{Key: "default_country_code", Type: "string"}}
	snap, err := r.Update(map[string]*module.LoadedModule{
		"contacts": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Name: "contacts", Type: "standard", JobTypes: jobTypes, ConfigSchema: configSchema},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	target, ok := ComputeTargets(snap)["contacts"]
	if !ok {
		t.Fatal("no compute target for contacts")
	}
	if len(target.JobTypes) != 1 || target.JobTypes[0].Name != "contacts_import" {
		t.Errorf("JobTypes = %+v, want %+v", target.JobTypes, jobTypes)
	}
	if len(target.ConfigSchema) != 1 || target.ConfigSchema[0].Key != "default_country_code" {
		t.Errorf("ConfigSchema = %+v, want %+v", target.ConfigSchema, configSchema)
	}
}

func TestRegistrySnapshot_ModelForTable(t *testing.T) {
	r := &ModuleRegistry{}
	lineItem := *model.Define("OrderLine")
	renamed := *model.Define("gadget", model.Table("legacy_gadgets"))
	snap, err := r.Update(map[string]*module.LoadedModule{
		"sales": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Type: "standard"},
			ModelDecls: []model.ModelDeclaration{lineItem, renamed},
		},
		"broken": {
			Status:     module.StatusFailed,
			Manifest:   manifest.Manifest{Type: "standard"},
			ModelDecls: []model.ModelDeclaration{*model.Define("thing")},
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	for table, want := range map[string]string{"order_line": "sales.OrderLine", "legacy_gadgets": "sales.gadget"} {
		if got, ok := snap.ModelForTable(table); !ok || got != want {
			t.Errorf("ModelForTable(%q) = %q, %v, want %q, true", table, got, ok, want)
		}
	}
	for _, table := range []string{"thing", "gadget", "unknown"} {
		if got, ok := snap.ModelForTable(table); ok {
			t.Errorf("ModelForTable(%q) = %q, true, want not found", table, got)
		}
	}
}

func TestRegistrySnapshot_RecordFormPath(t *testing.T) {
	snap := &RegistrySnapshot{schemaResponse: &SchemaResponse{Modules: map[string]*SchemaModule{
		"sales": {Routes: []SchemaRoute{
			{Method: "GET", Path: "/sales/orders", Model: "sales.order", CrudAction: "list", View: "order_list"},
			{Method: "PATCH", Path: "/sales/orders/{id}", Model: "sales.order", CrudAction: "update", View: "order_form"},
			{Method: "GET", Path: "/sales/orders/{id}", Model: "sales.order", CrudAction: "get", View: "order_form"},
			{Method: "GET", Path: "/sales/lines/{id}", Model: "sales.line", CrudAction: "get"},
		}},
	}}}

	tests := []struct {
		model, recordID, want string
	}{
		{"sales.order", "01j8", "/_m/sales/orders/01j8"},
		{"sales.line", "01j9", ""},      // no form view
		{"sales.invoice", "01ja", ""},   // no route
		{"contacts.person", "01jb", ""}, // no such module
		{"unqualified", "01jc", ""},
	}
	for _, tt := range tests {
		if got := snap.RecordFormPath(tt.model, tt.recordID); got != tt.want {
			t.Errorf("RecordFormPath(%q, %q) = %q, want %q", tt.model, tt.recordID, got, tt.want)
		}
	}
	if got := (&RegistrySnapshot{}).RecordFormPath("sales.order", "01j8"); got != "" {
		t.Errorf("RecordFormPath without a schema response = %q, want \"\"", got)
	}
}
