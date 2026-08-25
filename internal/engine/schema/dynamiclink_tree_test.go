package schema

import (
	"strings"
	"testing"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestToAtlasSchema_Tree_AddsCompanionPathColumn(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("category").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("parent_id", model.Many2One("contacts.category").Tree()),
	}

	s, err := ToAtlasSchema("tenant_acme", "contacts", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	table, ok := s.Table("category")
	if !ok {
		t.Fatal("category table not found")
	}

	col, ok := table.Column("parent_id_path")
	if !ok {
		t.Fatal("parent_id_path companion column not found")
	}
	ut, ok := col.Type.Type.(*postgres.UserDefinedType)
	if !ok {
		t.Fatalf("parent_id_path column type = %T, want *postgres.UserDefinedType", col.Type.Type)
	}
	if ut.T != "ltree" {
		t.Errorf("parent_id_path type = %q, want ltree", ut.T)
	}
	if !col.Type.Null {
		t.Error("expected parent_id_path to be nullable")
	}
}

func TestToAtlasSchema_Tree_NonSelfReferential_Errors(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("category").
			Field("id", model.UUID().Required().PrimaryKey()).
			// Points at a different model, not "contacts.category" —
			// .Tree() requires a self-referential Many2One.
			Field("parent_id", model.Many2One("contacts.contact").Tree()),
	}

	_, err := ToAtlasSchema("tenant_acme", "contacts", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error for a non-self-referential .Tree() field")
	}
	if !strings.Contains(err.Error(), "Tree()") {
		t.Errorf("error = %v, want it to mention Tree()", err)
	}
}

func TestToAtlasSchema_Tree_SelfReferential_NoError(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("category").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("parent_id", model.Many2One("contacts.category").Tree()),
	}

	if _, err := ToAtlasSchema("tenant_acme", "contacts", modelDecls, nil); err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
}

func TestToAtlasSchema_DynamicLink_ColumnTypeIsUUID_NoForeignKey(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("comment").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("reference_type", model.Selection("sales.order", "contacts.contact")).
			Field("reference_id", model.DynamicLink("reference_type")),
	}

	s, err := ToAtlasSchema("tenant_acme", "comments", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	table, _ := s.Table("comment")

	col, ok := table.Column("reference_id")
	if !ok {
		t.Fatal("reference_id column not found")
	}
	if _, ok := col.Type.Type.(*schema.UUIDType); !ok {
		t.Errorf("reference_id column type = %T, want *schema.UUIDType", col.Type.Type)
	}
	if len(table.ForeignKeys) != 0 {
		t.Errorf("got %d foreign keys, want 0 (DynamicLink has no FK)", len(table.ForeignKeys))
	}
}

func TestToAtlasSchema_DynamicLink_AddsCompositeIndex(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("comment").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("reference_type", model.Selection("sales.order", "contacts.contact")).
			Field("reference_id", model.DynamicLink("reference_type")),
	}

	s, err := ToAtlasSchema("tenant_acme", "comments", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	table, _ := s.Table("comment")

	var found bool
	for _, idx := range table.Indexes {
		if len(idx.Parts) != 2 {
			continue
		}
		names := []string{idx.Parts[0].C.Name, idx.Parts[1].C.Name}
		if names[0] == "reference_type" && names[1] == "reference_id" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an auto-declared composite index on (reference_type, reference_id), indexes = %+v", table.Indexes)
	}
}
