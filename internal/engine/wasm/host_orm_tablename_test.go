package wasm

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// TestTableNameForORM_MatchesSchemaPackage pins host_orm.go's local
// tableNameForORM against schema.TableNameFor — the two must never
// diverge, since host.orm resolves the same table schema sync already
// created. Duplicated locally rather than imported (see tableNameForORM's
// own doc comment for the import-cycle reason); this test is what keeps
// the duplication honest.
func TestTableNameForORM_MatchesSchemaPackage(t *testing.T) {
	cases := []model.ModelDeclaration{
		{Name: "widget"},
		{Name: "widget", Table: "custom_widgets"},
		{Name: "SalesOrder"},
		{Name: "sales.order"},
		{Name: "purchaseOrderLine"},
	}
	for _, md := range cases {
		got := tableNameForORM(md)
		want := schema.TableNameFor(md)
		if got != want {
			t.Errorf("tableNameForORM(%+v) = %q, want %q (schema.TableNameFor)", md, got, want)
		}
	}
}
