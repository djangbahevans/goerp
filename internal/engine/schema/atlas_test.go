package schema

import (
	"testing"

	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestToAtlasSchema_EnumField_ResolvesAgainstDeclaredType(t *testing.T) {
	typeDecls := []model.TypeDeclaration{
		model.EnumType("order_state_enum", "draft", "confirmed", "done", "cancelled"),
	}
	modelDecls := []model.ModelDeclaration{
		*model.Define("sales.order", model.Table("sales_orders")).
			WithStandardFields().
			Field("state", model.Enum("order_state_enum").Required()),
	}

	s, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, typeDecls)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}

	tbl, ok := s.Table("sales_orders")
	if !ok {
		t.Fatal("sales_orders table not found")
	}
	col, ok := tbl.Column("state")
	if !ok {
		t.Fatal("state column not found")
	}
	enum, ok := col.Type.Type.(*schema.EnumType)
	if !ok {
		t.Fatalf("state column type = %T, want *schema.EnumType", col.Type.Type)
	}
	if enum.T != "order_state_enum" {
		t.Errorf("enum name = %q, want %q", enum.T, "order_state_enum")
	}
	wantValues := []string{"draft", "confirmed", "done", "cancelled"}
	if len(enum.Values) != len(wantValues) {
		t.Fatalf("got %d enum values, want %d", len(enum.Values), len(wantValues))
	}
	for i, v := range wantValues {
		if enum.Values[i] != v {
			t.Errorf("enum value %d = %q, want %q", i, enum.Values[i], v)
		}
	}

	// The enum type must also be a schema object (what Atlas's planner
	// emits CREATE TYPE for), not just a column type — same *EnumType
	// instance, not a copy.
	found := false
	for _, obj := range s.Objects {
		if e, ok := obj.(*schema.EnumType); ok && e == enum {
			found = true
		}
	}
	if !found {
		t.Error("enum type not present in schema.Objects (or not the same instance as the column's type)")
	}
}

func TestToAtlasSchema_EnumField_UndeclaredTypeErrors(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("sales.order", model.Table("sales_orders")).
			WithStandardFields().
			Field("state", model.Enum("order_state_enum").Required()),
	}

	_, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error for a field referencing an undeclared enum type")
	}
}

func TestToAtlasSchema_RelationsResolveForEitherDeclarationSpelling(t *testing.T) {
	for _, names := range [][2]string{{"customer", "order"}, {"sales.customer", "sales.order"}} {
		customer := model.Define(names[0], model.Table("customers")).WithStandardFields().
			Field("order_ids", model.One2Many("sales.order", "customer_id"))
		order := model.Define(names[1], model.Table("orders")).WithStandardFields().
			Field("customer_id", model.Many2One("sales.customer")).
			Field("parent_id", model.Many2One("sales.order").Tree())

		s, err := ToAtlasSchema("tenant_acme", "sales", []model.ModelDeclaration{*customer, *order}, nil)
		if err != nil {
			t.Fatalf("declared as %v: ToAtlasSchema() error: %v", names, err)
		}
		tbl, ok := s.Table("orders")
		if !ok || len(tbl.ForeignKeys) != 2 {
			t.Fatalf("declared as %v: orders foreign keys = %v, want 2", names, tbl)
		}
	}
}
