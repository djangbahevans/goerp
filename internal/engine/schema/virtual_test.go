package schema

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestToAtlasSchema_Virtual_ProducesNoTable(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("legacy.inventory_item", model.Virtual()).
			Field("sku", model.Char()),
		*model.Define("sales.order", model.Table("orders")).
			Field("id", model.UUID().Required().PrimaryKey()),
	}

	s, err := ToAtlasSchema("tenant_acme", "legacy", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}

	if len(s.Tables) != 1 {
		t.Fatalf("got %d tables, want 1 (only the Table-backed model)", len(s.Tables))
	}
	if _, ok := s.Table("orders"); !ok {
		t.Error("expected the Table-backed model's own table to exist")
	}
	if _, ok := s.Table(TableNameFor(modelDecls[0])); ok {
		t.Error("expected no table for the Virtual-backed model")
	}
}
