package tenantexport

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func testModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "widget",
		Table: "widgets",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text().Required()},
			{Name: "credit_limit", Def: model.Integer().Access(model.AccessRead("contacts:contact:financials_read"))},
			{Name: "computed_display", Def: model.Text()}, // IsComputed set below via struct literal for clarity
			{Name: "children", Def: model.FieldDef{Kind: model.KindOne2Many, RelatedModel: "testmodule.child", InverseField: "parent_id"}},
		},
	}
}

func TestExportableColumns_ExcludesRestrictedOne2ManyAndUnstoredComputed(t *testing.T) {
	md := testModelDecl()
	// Mark computed_display as an unstored computed field directly.
	for i, f := range md.Fields {
		if f.Name == "computed_display" {
			f.Def.IsComputed = true
			f.Def.IsStored = false
			md.Fields[i] = f
		}
	}

	cols := exportableColumns(md)

	want := map[string]bool{"id": true, "name": true}
	if len(cols) != len(want) {
		t.Fatalf("exportableColumns = %v, want exactly %v", cols, want)
	}
	for _, c := range cols {
		if !want[c] {
			t.Errorf("unexpected column %q in %v", c, cols)
		}
	}
}

func TestExportableColumns_StoredComputedFieldIncluded(t *testing.T) {
	md := model.ModelDeclaration{
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().PrimaryKey()},
			{Name: "total", Def: model.Integer().Store(true)},
		},
	}
	for i, f := range md.Fields {
		if f.Name == "total" {
			f.Def.IsComputed = true
			md.Fields[i] = f
		}
	}

	cols := exportableColumns(md)
	found := false
	for _, c := range cols {
		if c == "total" {
			found = true
		}
	}
	if !found {
		t.Errorf("exportableColumns = %v, want to include stored computed field %q", cols, "total")
	}
}

func TestPrimaryKeyColumn(t *testing.T) {
	md := testModelDecl()
	pk, ok := primaryKeyColumn(md)
	if !ok || pk != "id" {
		t.Errorf("primaryKeyColumn() = (%q, %v), want (\"id\", true)", pk, ok)
	}

	noKey := model.ModelDeclaration{Fields: []model.NamedField{{Name: "x", Def: model.Text()}}}
	if _, ok := primaryKeyColumn(noKey); ok {
		t.Error("primaryKeyColumn() = ok for a model with no declared primary key")
	}
}

func TestTableNameFor(t *testing.T) {
	explicit := model.ModelDeclaration{Name: "widget", Table: "widgets"}
	if got := tableNameFor(explicit); got != "widgets" {
		t.Errorf("tableNameFor(explicit Table) = %q, want %q", got, "widgets")
	}

	derived := model.ModelDeclaration{Name: "SalesOrder"}
	if got := tableNameFor(derived); got != "sales_order" {
		t.Errorf("tableNameFor(no Table) = %q, want %q", got, "sales_order")
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent("widgets"); got != `"widgets"` {
		t.Errorf("quoteIdent(%q) = %q, want %q", "widgets", got, `"widgets"`)
	}
}
