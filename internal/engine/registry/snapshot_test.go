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
