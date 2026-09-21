package tenantprovision

import (
	"context"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

func loadedModule(name string, status module.ModuleStatus, dependsOn ...string) *module.LoadedModule {
	return &module.LoadedModule{Manifest: manifest.Manifest{Name: name, DependsOn: dependsOn}, Status: status}
}

func TestListModuleNames_DependencyOrder(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"accounting": loadedModule("accounting", module.StatusReady, "contacts"),
		"contacts":   loadedModule("contacts", module.StatusReady),
		"sales":      loadedModule("sales", module.StatusReady, "contacts", "accounting"),
		"broken":     loadedModule("broken", module.StatusFailed),
	}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}

	got, err := (&Activities{registry: reg}).ListModuleNames(context.Background())
	if err != nil {
		t.Fatalf("ListModuleNames() error: %v", err)
	}
	want := []string{"contacts", "accounting", "sales"}
	if !slices.Equal(got, want) {
		t.Fatalf("ListModuleNames() = %v, want %v: a module after the modules it depends on, failed modules left out", got, want)
	}
}
