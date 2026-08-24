package schema

import (
	"testing"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestToAtlasSchema_Sequence_ColumnTypeIsBigInt(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("invoice").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("number", model.Sequence("INV-{year}-")),
	}

	s, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	invoiceTable, ok := s.Table("invoice")
	if !ok {
		t.Fatal("invoice table not found")
	}
	col, ok := invoiceTable.Column("number")
	if !ok {
		t.Fatal("number column not found")
	}
	it, ok := col.Type.Type.(*schema.IntegerType)
	if !ok {
		t.Fatalf("number column type = %T, want *schema.IntegerType", col.Type.Type)
	}
	if it.T != postgres.TypeBigInt {
		t.Errorf("number column integer type = %q, want %q", it.T, postgres.TypeBigInt)
	}
}
