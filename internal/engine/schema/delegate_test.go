package schema

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestDelegateSchema(t *testing.T) {
	decls := []model.ModelDeclaration{
		*model.Define("employee").WithStandardFields().Field("contact_id", model.Delegate("hr.contact").Required()),
		*model.Define("contact").WithStandardFields().Field("name", model.Text()),
	}
	s, err := ToAtlasSchema("tenant_delegate", "hr", decls, nil)
	if err != nil {
		t.Fatal(err)
	}
	table, ok := s.Table("employee")
	if !ok {
		t.Fatal("employee table missing")
	}
	column, ok := table.Column("contact_id")
	if !ok || column.Type.Null || len(table.ForeignKeys) != 1 || table.ForeignKeys[0].RefTable.Name != "contact" {
		t.Fatalf("Delegate must store a required UUID FK: %+v", table)
	}
	if _, ok := table.Column("name"); ok {
		t.Fatal("delegated fields must not create columns on the composing table")
	}
}
