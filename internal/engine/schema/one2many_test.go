package schema

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestToAtlasSchema_One2Many_ValidInverse_NoColumnOrFK(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("address_ids", model.One2Many("contacts.address", "contact_id")),
		*model.Define("address").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("contact_id", model.Many2One("contacts.contact")),
	}

	s, err := ToAtlasSchema("tenant_acme", "contacts", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}

	contactTable, ok := s.Table("contact")
	if !ok {
		t.Fatal("contact table not found")
	}
	if _, ok := contactTable.Column("address_ids"); ok {
		t.Error("expected no address_ids column on the contact table")
	}
	if len(contactTable.ForeignKeys) != 0 {
		t.Errorf("expected no foreign keys on the contact table, got %+v", contactTable.ForeignKeys)
	}
}

func TestToAtlasSchema_One2Many_CrossModuleRelationRejected(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("address_ids", model.One2Many("shipping.address", "contact_id")), // different module
	}

	_, err := ToAtlasSchema("tenant_acme", "contacts", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error for a One2Many field targeting another module's model")
	}
}

func TestToAtlasSchema_One2Many_UnknownRelatedModelRejected(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("address_ids", model.One2Many("contacts.address", "contact_id")), // "address" never declared
	}

	_, err := ToAtlasSchema("tenant_acme", "contacts", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error for a One2Many field targeting an undeclared model")
	}
}

func TestToAtlasSchema_One2Many_MissingInverseFieldRejected(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("address_ids", model.One2Many("contacts.address", "contact_id")),
		*model.Define("address").
			Field("id", model.UUID().Required().PrimaryKey()), // no contact_id field
	}

	_, err := ToAtlasSchema("tenant_acme", "contacts", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error when the named inverse field doesn't exist")
	}
}

func TestToAtlasSchema_One2Many_InverseFieldNotMany2OneRejected(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("address_ids", model.One2Many("contacts.address", "contact_id")),
		*model.Define("address").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("contact_id", model.Text()), // not a Many2One
	}

	_, err := ToAtlasSchema("tenant_acme", "contacts", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error when the named inverse field isn't a Many2One")
	}
}

func TestToAtlasSchema_One2Many_InverseFieldPointsElsewhereRejected(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("address_ids", model.One2Many("contacts.address", "contact_id")),
		*model.Define("category").
			Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("address").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("contact_id", model.Many2One("contacts.category")), // points at the wrong model
	}

	_, err := ToAtlasSchema("tenant_acme", "contacts", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error when the named inverse field points at a different model")
	}
}
