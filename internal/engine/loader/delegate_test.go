package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestDelegateDependencies(t *testing.T) {
	for _, tc := range []struct {
		name string
		hard []string
		soft []string
		load bool
		fail bool
	}{
		{name: "hard", hard: []string{"contacts"}, load: true},
		{name: "soft", soft: []string{"contacts"}, load: true},
		{name: "unloaded soft", soft: []string{"contacts"}},
		{name: "undeclared", load: true, fail: true},
		{name: "missing hard", hard: []string{"contacts"}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modules := map[string]*module.LoadedModule{
				"hr": {
					Manifest:   manifest.Manifest{Name: "hr", DependsOn: tc.hard, SoftDependsOn: tc.soft},
					ModelDecls: []model.ModelDeclaration{*model.Define("employee").WithStandardFields().Field("contact_id", model.Delegate("contacts.contact"))},
				},
			}
			if tc.load {
				modules["contacts"] = &module.LoadedModule{Manifest: manifest.Manifest{Name: "contacts"}, ModelDecls: []model.ModelDeclaration{*model.Define("contact").WithStandardFields().Field("name", model.Text())}}
			}
			validateModelReferences(modules)
			if failed := modules["hr"].Status == module.StatusFailed; failed != tc.fail {
				t.Fatalf("failed=%v, want %v: %s", failed, tc.fail, modules["hr"].FailureReason)
			}
		})
	}
}

func TestDelegateRejectsCycle(t *testing.T) {
	m := &module.LoadedModule{
		Manifest: manifest.Manifest{Name: "hr"},
		ModelDecls: []model.ModelDeclaration{
			*model.Define("employee").WithStandardFields().Field("profile_id", model.Delegate("hr.profile")),
			*model.Define("profile").WithStandardFields().Field("employee_id", model.Delegate("hr.employee")),
		},
	}
	validateModelReferences(map[string]*module.LoadedModule{"hr": m})
	if m.Status != module.StatusFailed || !strings.Contains(m.FailureReason, "cycle") {
		t.Fatalf("cycle accepted: %s", m.FailureReason)
	}
}
