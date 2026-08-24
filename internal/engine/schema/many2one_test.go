package schema

import (
	"testing"

	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestToAtlasSchema_Many2One_AddsForeignKey(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("sales.contact").Label("Customer")),
	}

	s, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}

	orderTable, ok := s.Table("order")
	if !ok {
		t.Fatal("order table not found")
	}
	if len(orderTable.ForeignKeys) != 1 {
		t.Fatalf("got %d foreign keys, want 1", len(orderTable.ForeignKeys))
	}
	fk := orderTable.ForeignKeys[0]
	if fk.RefTable.Name != "contact" {
		t.Errorf("RefTable.Name = %q, want %q", fk.RefTable.Name, "contact")
	}
	if len(fk.Columns) != 1 || fk.Columns[0].Name != "customer_id" {
		t.Errorf("Columns = %+v, want [customer_id]", fk.Columns)
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0].Name != "id" {
		t.Errorf("RefColumns = %+v, want [id]", fk.RefColumns)
	}
}

func TestToAtlasSchema_Many2One_ColumnTypeIsUUID(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("sales.contact")),
	}

	s, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	orderTable, _ := s.Table("order")
	col, ok := orderTable.Column("customer_id")
	if !ok {
		t.Fatal("customer_id column not found")
	}
	if _, ok := col.Type.Type.(*schema.UUIDType); !ok {
		t.Errorf("customer_id column type = %T, want *schema.UUIDType", col.Type.Type)
	}
}

func TestToAtlasSchema_Many2One_DefaultOnDeleteIsRestrict(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("sales.contact")), // no .OnDelete() call
	}

	s, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	orderTable, _ := s.Table("order")
	if got := orderTable.ForeignKeys[0].OnDelete; got != schema.Restrict {
		t.Errorf("OnDelete = %q, want %q", got, schema.Restrict)
	}
}

func TestToAtlasSchema_Many2One_OnDeleteVariants(t *testing.T) {
	cases := []struct {
		name string
		b    model.OnDeleteBehaviour
		want schema.ReferenceOption
	}{
		{"SetNull", model.SetNull, schema.SetNull},
		{"Restrict", model.Restrict, schema.Restrict},
		{"Cascade", model.Cascade, schema.Cascade},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modelDecls := []model.ModelDeclaration{
				*model.Define("contact").
					Field("id", model.UUID().Required().PrimaryKey()),
				*model.Define("order").
					Field("id", model.UUID().Required().PrimaryKey()).
					Field("customer_id", model.Many2One("sales.contact").OnDelete(tc.b)),
			}
			s, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
			if err != nil {
				t.Fatalf("ToAtlasSchema() error: %v", err)
			}
			orderTable, _ := s.Table("order")
			if got := orderTable.ForeignKeys[0].OnDelete; got != tc.want {
				t.Errorf("OnDelete = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToAtlasSchema_Many2One_RejectsFieldNameWithoutIDSuffix(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer", model.Many2One("sales.contact")), // no _id suffix
	}

	_, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error for a Many2One field without an _id suffix")
	}
}

func TestToAtlasSchema_Many2One_SelfReferencing(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("employee").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("manager_id", model.Many2One("hr.employee")),
	}

	s, err := ToAtlasSchema("tenant_acme", "hr", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	tbl, _ := s.Table("employee")
	if len(tbl.ForeignKeys) != 1 || tbl.ForeignKeys[0].RefTable.Name != "employee" {
		t.Fatalf("expected a self-referencing FK, got %+v", tbl.ForeignKeys)
	}
}

func TestToAtlasSchema_Many2One_ForwardReferenceWithinSameModule(t *testing.T) {
	// "order" is declared before "contact" — the FK-adding pass must not
	// depend on declaration order.
	modelDecls := []model.ModelDeclaration{
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("sales.contact")),
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()),
	}

	s, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	orderTable, _ := s.Table("order")
	if len(orderTable.ForeignKeys) != 1 {
		t.Fatalf("expected 1 foreign key resolved despite declaration order, got %d", len(orderTable.ForeignKeys))
	}
}

func TestToAtlasSchema_Many2One_CrossModuleRelationRejected(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("contacts.contact")), // different module
	}

	_, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error for a Many2One field targeting another module's model")
	}
}

func TestToAtlasSchema_Many2One_BareUnqualifiedRelatedModelRejected(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("contact")), // missing "sales." prefix
	}

	_, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error for a related_model that isn't module-qualified")
	}
}

func TestToAtlasSchema_Many2One_TargetWithNoPrimaryKeyRejected(t *testing.T) {
	modelDecls := []model.ModelDeclaration{
		*model.Define("contact").
			Field("name", model.Text()), // no primary key
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("sales.contact")),
	}

	_, err := ToAtlasSchema("tenant_acme", "sales", modelDecls, nil)
	if err == nil {
		t.Fatal("expected an error when the target model declares no primary key")
	}
}
