package schema

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func crossModuleOrderDecls(relation model.FieldDef) []model.ModelDeclaration {
	return []model.ModelDeclaration{
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", relation),
	}
}

func TestToAtlasSchema_CrossModuleMany2One_DeclaredDependencyIsPlainColumn(t *testing.T) {
	tests := []struct {
		name string
		opt  AtlasOption
	}{
		{"hard dependency", WithDependencies([]string{"contacts"}, nil)},
		{"soft dependency", WithDependencies(nil, []string{"contacts"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact")), nil, tt.opt)
			if err != nil {
				t.Fatalf("ToAtlasSchema() error: %v", err)
			}
			order, ok := s.Table("order")
			if !ok {
				t.Fatal("order table not found")
			}
			if len(order.ForeignKeys) != 0 {
				t.Fatalf("got %d foreign keys, want none for a cross-module relation", len(order.ForeignKeys))
			}
			if _, ok := order.Column("customer_id"); !ok {
				t.Fatal("customer_id column not found")
			}
		})
	}
}

func TestToAtlasSchema_CrossModuleMany2One_ColumnTypeMatchesSameModule(t *testing.T) {
	same, err := ToAtlasSchema("tenant_acme", "sales", []model.ModelDeclaration{
		*model.Define("contact").Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("sales.contact")),
	}, nil)
	if err != nil {
		t.Fatalf("same-module ToAtlasSchema() error: %v", err)
	}
	cross, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact")), nil,
		WithDependencies([]string{"contacts"}, nil))
	if err != nil {
		t.Fatalf("cross-module ToAtlasSchema() error: %v", err)
	}

	sameOrder, _ := same.Table("order")
	crossOrder, _ := cross.Table("order")
	sameCol, _ := sameOrder.Column("customer_id")
	crossCol, _ := crossOrder.Column("customer_id")
	if !reflect.DeepEqual(sameCol.Type.Type, crossCol.Type.Type) {
		t.Fatalf("cross-module column type = %v, want the same-module type %v", crossCol.Type.Type, sameCol.Type.Type)
	}
}

func TestToAtlasSchema_CrossModuleMany2One_UndeclaredModuleRejected(t *testing.T) {
	_, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact")), nil,
		WithDependencies([]string{"billing"}, []string{"hr"}))
	if err == nil {
		t.Fatal("expected an error for a related_model in a module that is not a declared dependency")
	}
	for _, want := range []string{"order", "customer_id", "contacts.contact", "contacts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestToAtlasSchema_CrossModuleMany2One_NoDependenciesDeclaredRejected(t *testing.T) {
	if _, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact")), nil); err == nil {
		t.Fatal("expected an error when the module declares no dependencies")
	}
}

func TestToAtlasSchema_CrossModuleMany2One_OnDeleteActionRejected(t *testing.T) {
	for name, action := range map[string]model.OnDeleteBehaviour{"SetNull": model.SetNull, "Cascade": model.Cascade} {
		t.Run(name, func(t *testing.T) {
			_, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact").OnDelete(action)), nil,
				WithDependencies([]string{"contacts"}, nil))
			if err == nil {
				t.Fatal("expected an error for .OnDelete() on a relation with no foreign key")
			}
			if !strings.Contains(err.Error(), "customer_id") || !strings.Contains(err.Error(), "OnDelete") {
				t.Errorf("error %q does not name the field and .OnDelete()", err)
			}
		})
	}
}

func TestToAtlasSchema_CrossModuleMany2One_DefaultOnDeleteAccepted(t *testing.T) {
	if _, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact").OnDelete(model.Restrict)), nil,
		WithDependencies([]string{"contacts"}, nil)); err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
}

func TestToAtlasSchema_SameModuleMany2One_UnaffectedByDependencies(t *testing.T) {
	s, err := ToAtlasSchema("tenant_acme", "sales", []model.ModelDeclaration{
		*model.Define("contact").Field("id", model.UUID().Required().PrimaryKey()),
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("sales.contact").OnDelete(model.SetNull)),
	}, nil, WithDependencies([]string{"contacts"}, nil))
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	order, _ := s.Table("order")
	if len(order.ForeignKeys) != 1 {
		t.Fatalf("got %d foreign keys, want the same-module foreign key", len(order.ForeignKeys))
	}
}

func TestDiffAndExecute_CrossModuleMany2One_SyncsAsPlainColumn(t *testing.T) {
	for name, deps := range map[string][2][]string{
		"hard dependency": {{"contacts"}, nil},
		"soft dependency": {nil, {"contacts"}},
	} {
		t.Run(name, func(t *testing.T) {
			sess, engine := setupTenantSchema(t, "crossmodulem2o"+strings.ReplaceAll(name, " ", ""))
			sess.manifest.DependsOn = deps[0]
			sess.manifest.SoftDependsOn = deps[1]
			conn, _ := openTestPool(t, 5*time.Second)
			schemaName := "tenant_crossmodulem2o" + strings.ReplaceAll(name, " ", "")

			modelDecls := []model.ModelDeclaration{
				*model.Define("order", model.Table("orders")).
					Field("id", model.UUID().Required().PrimaryKey()).
					Field("customer_id", model.Many2One("contacts.contact")),
			}

			changes, err := engine.Diff(context.Background(), sess, modelDecls, nil)
			if err != nil {
				t.Fatalf("Diff() error: %v", err)
			}
			if _, _, err := engine.ExecuteAccepted(context.Background(), sess, modelDecls, changes, nil); err != nil {
				t.Fatalf("Execute() error: %v", err)
			}

			if !columnExists(t, conn, schemaName, "orders", "customer_id") {
				t.Fatal("customer_id column was not created")
			}
			var dataType string
			if err := conn.QueryRow(`SELECT data_type FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'orders' AND column_name = 'customer_id'`, schemaName).Scan(&dataType); err != nil {
				t.Fatalf("query column type: %v", err)
			}
			if dataType != "uuid" {
				t.Errorf("customer_id data_type = %q, want uuid", dataType)
			}
			var constraints int
			if err := conn.QueryRow(`SELECT count(*) FROM information_schema.table_constraints WHERE table_schema = $1 AND table_name = 'orders' AND constraint_type = 'FOREIGN KEY'`, schemaName).Scan(&constraints); err != nil {
				t.Fatalf("query foreign keys: %v", err)
			}
			if constraints != 0 {
				t.Errorf("foreign key count = %d, want 0", constraints)
			}
		})
	}
}
