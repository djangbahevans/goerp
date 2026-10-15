package schema

import (
	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func addFileForeignKey(s *schema.Schema, table *schema.Table, f model.NamedField) {
	if table == nil {
		return
	}
	column, ok := table.Column(f.Name)
	if !ok {
		return
	}
	// The engine owns files; the module's schema diff must not manage its table.
	id := schema.NewColumn("id").SetType(&schema.UUIDType{T: postgres.TypeUUID})
	files := schema.NewTable("files").SetSchema(s).AddColumns(id)
	table.AddForeignKeys(schema.NewForeignKey(table.Name + "_" + f.Name + "_fkey").
		SetTable(table).AddColumns(column).SetRefTable(files).AddRefColumns(id).SetOnDelete(schema.Restrict))
}
