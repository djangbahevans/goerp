package module

import (
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func loaded(name string, dependsOn ...string) *LoadedModule {
	return &LoadedModule{Manifest: manifest.Manifest{Name: name, DependsOn: dependsOn}}
}

func orderedNames(mods []*LoadedModule) []string {
	names := make([]string, len(mods))
	for i, m := range mods {
		names[i] = m.Manifest.Name
	}
	return names
}

func TestOrderByDependencies(t *testing.T) {
	tests := []struct {
		name string
		mods []*LoadedModule
		want []string
	}{
		{"name order when nothing depends on anything", []*LoadedModule{loaded("b"), loaded("a"), loaded("c")}, []string{"a", "b", "c"}},
		{"dependency before a module that sorts earlier", []*LoadedModule{loaded("accounting", "contacts"), loaded("contacts")}, []string{"contacts", "accounting"}},
		{"transitive dependencies", []*LoadedModule{loaded("a", "b"), loaded("b", "c"), loaded("c")}, []string{"c", "b", "a"}},
		{"shared dependency appears once", []*LoadedModule{loaded("a", "z"), loaded("b", "z"), loaded("z")}, []string{"z", "a", "b"}},
		{"dependency outside the set is ignored", []*LoadedModule{loaded("a", "missing"), loaded("b")}, []string{"a", "b"}},
		{"cycle is broken instead of looping", []*LoadedModule{loaded("a", "b"), loaded("b", "a")}, []string{"b", "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := orderedNames(OrderByDependencies(tt.mods)); !slices.Equal(got, tt.want) {
				t.Fatalf("order = %v, want %v", got, tt.want)
			}
		})
	}
}
