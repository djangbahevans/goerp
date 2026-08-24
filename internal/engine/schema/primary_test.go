package schema

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestToAtlasSchema_Primary_AllowsOnePerModel(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("name", model.Text().Required().Primary()),
	}

	if _, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil); err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
}

func TestToAtlasSchema_Primary_RejectsTwoPerModel(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("name", model.Text().Required().Primary()).
			Field("display_name", model.Text().Primary()),
	}

	_, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error for two .Primary() fields on the same model")
	}
}

func TestToAtlasSchema_Primary_NoneDeclaredIsFine(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("name", model.Text().Required()),
	}

	if _, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil); err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
}

func TestToAtlasSchema_Primary_ScopedPerModelNotAcrossModels(t *testing.T) {
	// Two different models each declaring their own .Primary() is fine —
	// the "at most one" rule is per-model, not per-schema.
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("name", model.Text().Required().Primary()),
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("reference", model.Text().Required().Primary()),
	}

	if _, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil); err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
}
