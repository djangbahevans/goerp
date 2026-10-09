package modelextension

import (
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func extensionModules() map[string]*module.LoadedModule {
	return map[string]*module.LoadedModule{
		"contacts": {
			Manifest: manifest.Manifest{
				Name: "contacts", Type: "domain",
				Schema: manifest.SchemaConfig{OwnedModels: []string{"contacts.contact"}},
			},
			ModelDecls: []model.ModelDeclaration{
				*model.Define("contact", model.Table("contacts")).WithStandardFields().Field("name", model.Text().Required()),
			},
		},
		"industry": {
			Manifest: manifest.Manifest{
				Name: "industry", Type: "field_extension", DependsOn: []string{"contacts"},
				Schema: manifest.SchemaConfig{ExtendsModule: new("contacts"), ExtendsModels: []string{"contacts.contact"}},
			},
			LoadOrder: 1,
			ModelExtensions: []model.ModelExtension{
				model.Extend("contacts.contact").Field("industry", model.Text()).Index("industry_idx", model.BTreeIndex("industry")),
			},
		},
	}
}

func TestResolveRejectsInvalidExtensions(t *testing.T) {
	cases := []struct {
		name   string
		modify func(map[string]*module.LoadedModule)
		want   string
	}{
		{name: "undeclared target", modify: func(m map[string]*module.LoadedModule) {
			m["industry"].ModelExtensions[0].Model = "contacts.missing"
		}, want: "contacts.missing"},
		{name: "wrong owner", modify: func(m map[string]*module.LoadedModule) {
			m["industry"].Manifest.Schema.ExtendsModule = new("sales")
		}, want: "contacts.contact"},
		{name: "missing dependency", modify: func(m map[string]*module.LoadedModule) {
			m["industry"].Manifest.DependsOn = nil
		}, want: "depends_on"},
		{name: "wrong module type", modify: func(m map[string]*module.LoadedModule) {
			m["industry"].Manifest.Type = "domain"
		}, want: "contacts.contact"},
		{name: "required without default", modify: func(m map[string]*module.LoadedModule) {
			m["industry"].ModelExtensions[0].Fields[0].Def = model.Text().Required()
		}, want: "nullable or have a default"},
		{name: "base field collision", modify: func(m map[string]*module.LoadedModule) {
			m["industry"].ModelExtensions[0].Fields[0].Name = "name"
		}, want: "already exists"},
		{name: "duplicate field", modify: func(m map[string]*module.LoadedModule) {
			m["industry"].ModelExtensions[0] = m["industry"].ModelExtensions[0].Field("industry", model.Text())
		}, want: "already exists"},
		{name: "virtual target", modify: func(m map[string]*module.LoadedModule) {
			m["contacts"].ModelDecls[0].Backend = model.BackendVirtual
		}, want: "table-backed"},
		{name: "failed owner", modify: func(m map[string]*module.LoadedModule) {
			m["contacts"].Status = module.StatusFailed
		}, want: "not loaded"},
		{name: "missing model", modify: func(m map[string]*module.LoadedModule) {
			m["contacts"].ModelDecls = nil
		}, want: "contacts.contact"},
		{name: "unknown index column", modify: func(m map[string]*module.LoadedModule) {
			m["industry"].ModelExtensions[0].Indexes[0].Def = model.BTreeIndex("missing")
		}, want: "unknown field"},
		{name: "primary key", modify: func(m map[string]*module.LoadedModule) {
			m["industry"].ModelExtensions[0].Fields[0].Def = model.UUID().PrimaryKey()
		}, want: "model identity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modules := extensionModules()
			tc.modify(modules)

			_, err := Resolve(modules)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Resolve error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolveKeepsDeclarationsImmutableAndRejectsLaterCollision(t *testing.T) {
	modules := extensionModules()
	resolved, err := Resolve(modules)
	if err != nil {
		t.Fatal(err)
	}
	baseFields := modules["contacts"].ModelDecls[0].Fields
	mergedFields := resolved["contacts"].ModelDecls[0].Fields
	if len(mergedFields) != len(baseFields)+1 || mergedFields[len(baseFields)].DeclaringModule != "industry" {
		t.Fatalf("merged fields = %+v", mergedFields)
	}
	if slices.ContainsFunc(baseFields, func(f model.NamedField) bool { return f.Name == "industry" }) {
		t.Fatal("raw declarations changed")
	}

	later := *modules["industry"]
	later.Manifest.Name = "other"
	later.LoadOrder = 2
	modules["other"] = &later
	_, err = Resolve(modules)
	if err == nil || !strings.Contains(err.Error(), `module "other"`) {
		t.Fatalf("later collision = %v", err)
	}
}
