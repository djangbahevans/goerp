package schema

import (
	"strings"
	"testing"
	"time"

	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func many2ManyModels() []model.ModelDeclaration {
	return []model.ModelDeclaration{
		*model.Define("contact", model.Table("contacts")).
			Field("id", model.UUID().PrimaryKey()).
			Field("tag_ids", model.Many2Many("testmodule.tag", model.Through("contact_tag_assignments"), model.M2MColumn1("contact_id"), model.M2MColumn2("tag_id"))),
		*model.Define("tag", model.Table("contact_tags")).
			Field("id", model.UUID().PrimaryKey()).
			Field("name", model.Text()),
	}
}

func TestToAtlasSchema_Many2Many(t *testing.T) {
	s, err := ToAtlasSchema("tenant_example", "testmodule", many2ManyModels(), nil)
	if err != nil {
		t.Fatal(err)
	}

	parent, _ := s.Table("contacts")
	if _, found := parent.Column("tag_ids"); found {
		t.Fatal("Many2Many must not create a scalar column")
	}

	join, found := s.Table("contact_tag_assignments")
	if !found || len(join.Columns) != 2 || len(join.PrimaryKey.Parts) != 2 || len(join.ForeignKeys) != 2 {
		t.Fatalf("invalid join table: %+v", join)
	}
	for _, fk := range join.ForeignKeys {
		if fk.OnDelete != schema.Cascade || fk.Columns[0].Type.Null {
			t.Fatalf("invalid join foreign key: %+v", fk)
		}
	}
}

func TestToAtlasSchema_Many2ManyValidation(t *testing.T) {
	cases := []struct {
		name   string
		modify func([]model.ModelDeclaration)
	}{
		{"missing through", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def.JoinTable = "" }},
		{"unsafe identifier", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def.Column1 = "public.id" }},
		{"same columns", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def.Column2 = "contact_id" }},
		{"table collision", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def.JoinTable = "contacts" }},
		{"reserved table", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def.JoinTable = "record_activity" }},
		{"unknown target", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def.RelatedModel = "testmodule.missing" }},
		{"bad suffix", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Name = "tags" }},
		{"virtual owner", func(ms []model.ModelDeclaration) { ms[0].Backend = model.BackendVirtual }},
		{"integer key", func(ms []model.ModelDeclaration) { ms[1].Fields[0].Def = model.Integer().PrimaryKey() }},
		{"computed", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def.IsComputed = true }},
		{"required", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def = ms[0].Fields[1].Def.Required() }},
		{"domain", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def = ms[0].Fields[1].Def.Domain("active") }},
		{"on delete", func(ms []model.ModelDeclaration) { ms[0].Fields[1].Def = ms[0].Fields[1].Def.OnDelete(model.Cascade) }},
		{"read key collision", func(ms []model.ModelDeclaration) {
			ms[0].Fields = append(ms[0].Fields, model.NamedField{Name: "tags", Def: model.Text()})
		}},
		{"duplicate join", func(ms []model.ModelDeclaration) {
			f := ms[0].Fields[1]
			f.Name = "other_ids"
			ms[0].Fields = append(ms[0].Fields, f)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			models := many2ManyModels()
			tc.modify(models)
			if _, err := ToAtlasSchema("tenant_example", "testmodule", models, nil); err == nil {
				t.Fatal("expected declaration rejection")
			}
		})
	}
}

func TestToAtlasSchema_Many2ManyHardDependency(t *testing.T) {
	models := many2ManyModels()
	models[0].Fields[1].Def.RelatedModel = "tags.tag"
	s, err := ToAtlasSchema("tenant_example", "testmodule", models[:1], nil,
		WithDependencies([]string{"tags"}, nil), WithDependencyModels(map[string][]model.ModelDeclaration{"tags": {models[1]}}))
	if err != nil {
		t.Fatal(err)
	}

	join, _ := s.Table("contact_tag_assignments")
	if len(s.Tables) != 2 || join.ForeignKeys[1].RefTable.Name != "contact_tags" {
		t.Fatalf("dependency table must only be referenced: %+v", s.Tables)
	}

	if _, err := ToAtlasSchema("tenant_example", "testmodule", models[:1], nil, WithDependencies(nil, []string{"tags"})); err == nil {
		t.Fatal("soft dependency cannot guarantee cascading foreign keys")
	}
}

func TestDiffAndExecute_Many2Many(t *testing.T) {
	slug := "m2m" + strings.ReplaceAll(t.Name(), "_", "")
	sess, engine := setupTenantSchema(t, strings.ToLower(slug))
	models := many2ManyModels()

	changes, err := engine.Diff(t.Context(), sess, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	if blocked, _, err := engine.ExecuteAccepted(t.Context(), sess, models, changes, nil); err != nil || len(blocked) != 0 {
		t.Fatalf("execute: %v; blocked: %v", err, blocked)
	}

	changes, err = engine.Diff(t.Context(), sess, models, nil)
	if err != nil || len(changes) != 0 {
		t.Fatalf("repeat sync: %v; changes: %v", err, changes)
	}

	conn, _ := openTestPool(t, 5*time.Second)
	prefix := quoteIdent("tenant_"+strings.ToLower(slug)) + "."
	for _, sql := range []string{
		"INSERT INTO " + prefix + "contacts VALUES ('11111111-1111-1111-1111-111111111111')",
		"INSERT INTO " + prefix + "contact_tags VALUES ('22222222-2222-2222-2222-222222222222', 'VIP')",
		"INSERT INTO " + prefix + "contact_tag_assignments VALUES ('11111111-1111-1111-1111-111111111111', '22222222-2222-2222-2222-222222222222')",
		"DELETE FROM " + prefix + "contact_tags",
	} {
		if _, err := conn.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}

	var count int
	if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM "+prefix+"contact_tag_assignments").Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade count = %d: %v", count, err)
	}
}
