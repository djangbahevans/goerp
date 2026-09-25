package schema

import (
	"testing"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestToAtlasSchema_Sequence_ColumnTypeIsText(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("invoice").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("number", model.Sequence("INV-{year}-{seq:05}")),
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
	st, ok := col.Type.Type.(*schema.StringType)
	if !ok {
		t.Fatalf("number column type = %T, want *schema.StringType", col.Type.Type)
	}
	if st.T != postgres.TypeText {
		t.Errorf("number column string type = %q, want %q", st.T, postgres.TypeText)
	}
}
