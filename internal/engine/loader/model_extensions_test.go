package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func modelExtensionModules() map[string]*module.LoadedModule {
	return map[string]*module.LoadedModule{
		"contacts": {
			Manifest: manifest.Manifest{
				Name:   "contacts",
				Type:   "domain",
				Schema: manifest.SchemaConfig{OwnedModels: []string{"contacts.contact"}},
			},
			ModelDecls: []model.ModelDeclaration{
				*model.Define("contact", model.Table("contacts")).WithStandardFields(),
			},
		},
		"industry": {
			Manifest: manifest.Manifest{
				Name:      "industry",
				Type:      "field_extension",
				DependsOn: []string{"contacts"},
				Schema: manifest.SchemaConfig{
					ExtendsModule: new("contacts"),
					ExtendsModels: []string{"contacts.contact"},
				},
			},
			ModelExtensions: []model.ModelExtension{
				model.Extend("contacts.contact").Field("industry", model.Text()),
			},
		},
	}
}

func TestValidateModelExtensionsIsolatesCollisionAndFailedDependencies(t *testing.T) {
	modules := modelExtensionModules()
	modules["industry"].LoadOrder = 1

	collision := *modules["industry"]
	collision.Manifest.Name = "collision"
	collision.LoadOrder = 2
	modules["collision"] = &collision
	modules["dependent"] = &module.LoadedModule{
		Manifest: manifest.Manifest{
			Name:      "dependent",
			Type:      "domain",
			DependsOn: []string{"collision"},
		},
	}

	ValidateModelExtensions(modules)

	if modules["contacts"].Status == module.StatusFailed || modules["industry"].Status == module.StatusFailed {
		t.Fatal("a rejected extension fails valid modules")
	}

	if collision.Status != module.StatusFailed || !strings.Contains(collision.FailureReason, "already exists") {
		t.Fatalf("colliding extension = %+v", collision)
	}

	if modules["dependent"].Status != module.StatusFailed {
		t.Fatal("dependent of a failed extension remains available")
	}
}

func TestValidateModelExtensionCandidateChecksTrackedFieldRules(t *testing.T) {
	modules := modelExtensionModules()
	candidate := modules["industry"]
	candidate.ModelExtensions = []model.ModelExtension{
		model.Extend("contacts.contact").Field("children", model.One2Many("contacts.contact", "parent_id").Tracked()),
	}

	err := ValidateModelExtensionCandidate(candidate, modules)
	if err == nil || !strings.Contains(err.Error(), ".Tracked() is not valid on a One2Many") {
		t.Fatalf("candidate validation = %v", err)
	}

	if len(modules["contacts"].ModelDecls[0].Fields) != 7 || modules["contacts"].Status == module.StatusFailed {
		t.Fatal("candidate validation changes the live owner")
	}
}
