package registry

import (
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestSchemaDelegateComposition(t *testing.T) {
	employee := *model.Define("employee").WithStandardFields().Field("contact_id", model.Delegate("contacts.contact"))
	contact := *model.Define("contact").WithStandardFields().Field("name", model.Text()).Field("category", model.Enum("contact_category")).
		Field("secret", model.Text().Access(model.AccessRead(perm.Ref("contacts:contact:secret"))))

	modules := map[string]*module.LoadedModule{
		"hr": {
			Manifest:   manifest.Manifest{Name: "hr", DependsOn: []string{"contacts"}},
			ModelDecls: []model.ModelDeclaration{employee},
		},
		"contacts": {
			Manifest:   manifest.Manifest{Name: "contacts"},
			TypeDecls:  []model.TypeDeclaration{model.EnumType("contact_category", "person", "organization")},
			ModelDecls: []model.ModelDeclaration{contact},
		},
	}
	rules := buildFieldSecRegistry(modules)
	if rule, ok := rules.Rule("hr.employee", "secret"); !ok || rule.ReadPermission != "contacts:contact:secret" {
		t.Fatalf("flattened field missing from permission metadata: %+v, %v", rule, ok)
	}

	schema := buildSchemaResponse(modules, route.New(), "hash")
	fields := map[string]SchemaField{}
	for _, f := range schema.Modules["hr"].Models["hr.employee"].Fields {
		fields[f.Name] = f
	}
	if !slices.Equal(fields["category"].SelectionValues, []string{"person", "organization"}) {
		t.Fatalf("delegated enum lost the target's values: %+v", fields["category"])
	}
	if fields["contact_id"].Type != "delegate" || fields["contact_id"].RelatedModel != "contacts.contact" || fields["contact_id"].ReadKey != "" || fields["name"].Type != "text" {
		t.Fatalf("incomplete delegate schema: %+v", fields)
	}
}
