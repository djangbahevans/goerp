package schema

import (
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestMany2ManyExtensionSyncWithEffectiveModels(t *testing.T) {
	slug := "m2mextension" + uuid.NewV7().String()[24:]
	baseSession, _, conn := setupTenantSchemaForModule(t, slug, "contacts")
	models := []model.ModelDeclaration{
		*model.Define("contact", model.Table("contacts")).Field("id", model.UUID().PrimaryKey()),
		*model.Define("tag", model.Table("tags")).Field("id", model.UUID().PrimaryKey()),
	}
	source := fakeModelSource{"contacts": models}
	engine := NewSchemaDiffEngine(&Config{ModelSource: source})
	changes, err := engine.Diff(t.Context(), baseSession, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := engine.ExecuteAccepted(t.Context(), baseSession, models, changes, nil); err != nil {
		t.Fatal(err)
	}
	if err := baseSession.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	field := model.NamedField{
		Name:            "tag_ids",
		Def:             model.Many2Many("contacts.tag", model.Through("industry_contact_tags"), model.M2MColumn1("contact_id"), model.M2MColumn2("tag_id")),
		DeclaringModule: "industry",
	}
	models[0].Fields = append(models[0].Fields, field)
	source["contacts"] = models
	pool := NewPool(conn, time.Second)
	mf := &manifest.Manifest{
		Name: "industry", Type: "field_extension", Version: "1.0.0", DependsOn: []string{"contacts"},
		Schema: manifest.SchemaConfig{ExtendsModule: new("contacts"), ExtendsModels: []string{"contacts.contact"}},
	}
	sess, err := pool.BeginSync(t.Context(), uuid.NewV7().String(), slug, "industry", mf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close(t.Context()) }()
	ext := model.Extend("contacts.contact").Field(field.Name, field.Def)
	changes, err = engine.Diff(t.Context(), sess, nil, nil, ext)
	if err != nil {
		t.Fatal(err)
	}
	if blocked, _, err := engine.ExecuteAccepted(t.Context(), sess, nil, changes, nil); err != nil || len(blocked) != 0 {
		t.Fatalf("extension sync: blocked=%v, error=%v", blocked, err)
	}
	changes, err = engine.Diff(t.Context(), sess, nil, nil, ext)
	if err != nil || len(changes) != 0 {
		t.Fatalf("repeat extension sync: %v; changes=%v", err, changes)
	}
}
