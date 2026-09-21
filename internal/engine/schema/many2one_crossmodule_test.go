package schema

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func crossModuleOrderDecls(relation model.FieldDef) []model.ModelDeclaration {
	return []model.ModelDeclaration{
		*model.Define("order").
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", relation),
	}
}

func contactsDecls() []model.ModelDeclaration {
	return []model.ModelDeclaration{
		*model.Define("contact", model.Table("contacts")).Field("id", model.UUID().Required().PrimaryKey()),
	}
}

type fakeModelSource map[string][]model.ModelDeclaration

func (f fakeModelSource) ModelDeclarations(name string) ([]model.ModelDeclaration, bool) {
	decls, ok := f[name]
	return decls, ok
}

func TestToAtlasSchema_CrossModuleMany2One_HardDependencyGetsForeignKeyToStandIn(t *testing.T) {
	s, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact").OnDelete(model.SetNull)), nil,
		WithDependencies([]string{"contacts"}, nil),
		WithDependencyModels(map[string][]model.ModelDeclaration{"contacts": contactsDecls()}))
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}

	order, _ := s.Table("order")
	if len(order.ForeignKeys) != 1 {
		t.Fatalf("got %d foreign keys, want 1", len(order.ForeignKeys))
	}
	fk := order.ForeignKeys[0]
	if fk.RefTable.Name != "contacts" || fk.RefTable.Schema.Name != "tenant_acme" {
		t.Errorf("RefTable = %s.%s, want tenant_acme.contacts", fk.RefTable.Schema.Name, fk.RefTable.Name)
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0].Name != "id" {
		t.Errorf("RefColumns = %+v, want [id]", fk.RefColumns)
	}
	if fk.OnDelete != schema.SetNull {
		t.Errorf("OnDelete = %v, want SetNull", fk.OnDelete)
	}

	if len(s.Tables) != 1 {
		t.Fatalf("schema has %d tables, want only the module's own: the referenced table is another module's", len(s.Tables))
	}
	if _, present := s.Table("contacts"); present {
		t.Error("the referenced table must not be part of the module's schema")
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
		WithDependencies(nil, []string{"contacts"}))
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

func TestToAtlasSchema_CrossModuleMany2One_PlainColumnCases(t *testing.T) {
	loaded := map[string][]model.ModelDeclaration{"contacts": contactsDecls()}
	virtual := map[string][]model.ModelDeclaration{"contacts": {
		*model.Define("contact", model.Virtual()).Field("id", model.UUID().Required().PrimaryKey()),
	}}
	tests := []struct {
		name string
		opts []AtlasOption
	}{
		{"soft dependency that is loaded", []AtlasOption{WithDependencies(nil, []string{"contacts"}), WithDependencyModels(loaded)}},
		{"soft dependency that is not loaded", []AtlasOption{WithDependencies(nil, []string{"contacts"})}},
		{"hard dependency whose model has no table", []AtlasOption{WithDependencies([]string{"contacts"}, nil), WithDependencyModels(virtual)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact")), nil, tt.opts...)
			if err != nil {
				t.Fatalf("ToAtlasSchema() error: %v", err)
			}
			order, _ := s.Table("order")
			if len(order.ForeignKeys) != 0 {
				t.Fatalf("got %d foreign keys, want none", len(order.ForeignKeys))
			}
			if _, ok := order.Column("customer_id"); !ok {
				t.Fatal("customer_id column not found")
			}

			if _, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact").OnDelete(model.SetNull)), nil, tt.opts...); err == nil {
				t.Error("expected an error for .OnDelete() on a relation with no foreign key")
			}
		})
	}
}

func TestToAtlasSchema_CrossModuleMany2One_Rejected(t *testing.T) {
	loaded := map[string][]model.ModelDeclaration{"contacts": contactsDecls()}
	noPK := map[string][]model.ModelDeclaration{"contacts": {*model.Define("contact", model.Table("contacts")).Field("name", model.Text())}}
	tests := []struct {
		name    string
		related string
		opts    []AtlasOption
		want    []string
	}{
		{"undeclared module", "contacts.contact", []AtlasOption{WithDependencies([]string{"billing"}, []string{"hr"})}, []string{"customer_id", "contacts.contact", "not in depends_on"}},
		{"no dependencies declared", "contacts.contact", nil, []string{"customer_id", "not in depends_on"}},
		{"model missing from a loaded hard dependency", "contacts.company", []AtlasOption{WithDependencies([]string{"contacts"}, nil), WithDependencyModels(loaded)}, []string{"customer_id", "contacts.company", "not declared"}},
		{"model missing from a loaded soft dependency", "contacts.company", []AtlasOption{WithDependencies(nil, []string{"contacts"}), WithDependencyModels(loaded)}, []string{"customer_id", "contacts.company", "not declared"}},
		{"hard dependency that is not loaded", "contacts.contact", []AtlasOption{WithDependencies([]string{"contacts"}, nil)}, []string{"customer_id", "hard dependency", "not loaded"}},
		{"target without a primary key", "contacts.contact", []AtlasOption{WithDependencies([]string{"contacts"}, nil), WithDependencyModels(noPK)}, []string{"customer_id", "no primary key"}},
		{"no module part", "contact", []AtlasOption{WithDependencies([]string{"contacts"}, nil)}, []string{"customer_id", "module-qualified"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One(tt.related)), nil, tt.opts...)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
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

func crossModuleForeignKeys(t *testing.T, conn *sql.DB, schemaName string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM information_schema.table_constraints WHERE table_schema = $1 AND table_name = 'orders' AND constraint_type = 'FOREIGN KEY'`, schemaName).Scan(&n); err != nil {
		t.Fatalf("query foreign keys: %v", err)
	}
	return n
}

func syncCrossModuleOrders(t *testing.T, engine *SchemaDiffEngine, sess *SchemaSyncSession, relation model.FieldDef) {
	t.Helper()
	decls := []model.ModelDeclaration{
		*model.Define("order", model.Table("orders")).
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", relation),
	}
	changes, err := engine.Diff(context.Background(), sess, decls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, decls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
}

func TestDiffAndExecute_CrossModuleMany2One_HardDependencyCreatesForeignKey(t *testing.T) {
	const slug = "crossmodulefk"
	sess, _ := setupTenantSchema(t, slug)
	sess.manifest.DependsOn = []string{"contacts"}
	engine := NewSchemaDiffEngine(&Config{ModelSource: fakeModelSource{"contacts": contactsDecls()}})
	conn, _ := openTestPool(t, 5*time.Second)
	schemaName := "tenant_" + slug

	if _, err := conn.Exec(`CREATE TABLE ` + schemaName + `.contacts (id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create the contacts module's table: %v", err)
	}
	syncCrossModuleOrders(t, engine, sess, model.Many2One("contacts.contact").OnDelete(model.SetNull))

	if got := crossModuleForeignKeys(t, conn, schemaName); got != 1 {
		t.Fatalf("foreign key count = %d, want 1", got)
	}
	if _, err := conn.Exec(`INSERT INTO ` + schemaName + `.orders (id, customer_id) VALUES (gen_random_uuid(), gen_random_uuid())`); err == nil {
		t.Fatal("expected a foreign key violation for a customer_id with no contact, got none")
	}

	const contactID = "22222222-2222-2222-2222-222222222222"
	if _, err := conn.Exec(`INSERT INTO ` + schemaName + `.contacts (id) VALUES ('` + contactID + `')`); err != nil {
		t.Fatalf("insert contact: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO ` + schemaName + `.orders (id, customer_id) VALUES (gen_random_uuid(), '` + contactID + `')`); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	if _, err := conn.Exec(`DELETE FROM ` + schemaName + `.contacts WHERE id = '` + contactID + `'`); err != nil {
		t.Fatalf("delete contact: %v", err)
	}
	var customer sql.NullString
	if err := conn.QueryRow(`SELECT customer_id::text FROM ` + schemaName + `.orders`).Scan(&customer); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if customer.Valid {
		t.Errorf("customer_id = %q after the contact was deleted, want NULL from ON DELETE SET NULL", customer.String)
	}
}

func TestDiffAndExecute_CrossModuleMany2One_SoftDependencyCreatesNoForeignKey(t *testing.T) {
	const slug = "crossmodulesoft"
	sess, _ := setupTenantSchema(t, slug)
	sess.manifest.SoftDependsOn = []string{"contacts"}
	engine := NewSchemaDiffEngine(&Config{ModelSource: fakeModelSource{}})
	conn, _ := openTestPool(t, 5*time.Second)
	schemaName := "tenant_" + slug

	syncCrossModuleOrders(t, engine, sess, model.Many2One("contacts.contact"))

	if got := crossModuleForeignKeys(t, conn, schemaName); got != 0 {
		t.Fatalf("foreign key count = %d, want 0", got)
	}
	var dataType string
	if err := conn.QueryRow(`SELECT data_type FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'orders' AND column_name = 'customer_id'`, schemaName).Scan(&dataType); err != nil {
		t.Fatalf("query column type: %v", err)
	}
	if dataType != "uuid" {
		t.Errorf("customer_id data_type = %q, want uuid", dataType)
	}
}

func TestDiffAndExecute_CrossModuleMany2One_SoftToHardAddsForeignKeyNotValidOverOrphans(t *testing.T) {
	const slug = "crossmodulepromote"
	sess, _ := setupTenantSchema(t, slug)
	conn, _ := openTestPool(t, 5*time.Second)
	schemaName := "tenant_" + slug
	engine := NewSchemaDiffEngine(&Config{ModelSource: fakeModelSource{"contacts": contactsDecls()}})

	if _, err := conn.Exec(`CREATE TABLE ` + schemaName + `.contacts (id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create the contacts module's table: %v", err)
	}
	sess.manifest.SoftDependsOn = []string{"contacts"}
	syncCrossModuleOrders(t, engine, sess, model.Many2One("contacts.contact"))
	if _, err := conn.Exec(`INSERT INTO ` + schemaName + `.orders (id, customer_id) VALUES (gen_random_uuid(), gen_random_uuid())`); err != nil {
		t.Fatalf("seed an order with no contact: %v", err)
	}

	sess.manifest.SoftDependsOn = nil
	sess.manifest.DependsOn = []string{"contacts"}
	syncCrossModuleOrders(t, engine, sess, model.Many2One("contacts.contact"))

	if got := crossModuleForeignKeys(t, conn, schemaName); got != 1 {
		t.Fatalf("foreign key count = %d, want 1", got)
	}
	if constraintValid(t, conn, schemaName, "orders_customer_id_fkey") {
		t.Error("constraint is already validated over an order with no contact; want NOT VALID until the validation job runs")
	}
	if _, _, found := pendingValidationStatus(t, conn, "44444444-4444-4444-4444-444444444444", "orders", "orders_customer_id_fkey"); !found {
		t.Error("no pending validation recorded for the new foreign key")
	}
}

func TestDiffAndExecute_CrossModuleMany2One_HardToSoftDropsForeignKey(t *testing.T) {
	const slug = "crossmoduledemote"
	sess, _ := setupTenantSchema(t, slug)
	conn, _ := openTestPool(t, 5*time.Second)
	schemaName := "tenant_" + slug
	engine := NewSchemaDiffEngine(&Config{ModelSource: fakeModelSource{"contacts": contactsDecls()}})

	if _, err := conn.Exec(`CREATE TABLE ` + schemaName + `.contacts (id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create the contacts module's table: %v", err)
	}
	sess.manifest.DependsOn = []string{"contacts"}
	syncCrossModuleOrders(t, engine, sess, model.Many2One("contacts.contact"))
	if got := crossModuleForeignKeys(t, conn, schemaName); got != 1 {
		t.Fatalf("foreign key count = %d, want 1 before the demotion", got)
	}

	sess.manifest.DependsOn = nil
	sess.manifest.SoftDependsOn = []string{"contacts"}
	syncCrossModuleOrders(t, engine, sess, model.Many2One("contacts.contact"))
	if got := crossModuleForeignKeys(t, conn, schemaName); got != 0 {
		t.Errorf("foreign key count = %d, want 0 after the dependency became soft", got)
	}
}

func TestDiffAndExecute_CrossModuleMany2One_ForeignKeyNeverTouchesReferencedTable(t *testing.T) {
	const slug = "crossmoduleref"
	sess, _ := setupTenantSchema(t, slug)
	sess.manifest.DependsOn = []string{"contacts"}
	engine := NewSchemaDiffEngine(&Config{ModelSource: fakeModelSource{"contacts": contactsDecls()}})
	conn, _ := openTestPool(t, 5*time.Second)
	schemaName := "tenant_" + slug

	if _, err := conn.Exec(`CREATE TABLE ` + schemaName + `.contacts (id uuid PRIMARY KEY, display_name text NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create the contacts module's table: %v", err)
	}
	decls := []model.ModelDeclaration{
		*model.Define("order", model.Table("orders")).
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("contacts.contact")),
	}
	changes, err := engine.Diff(context.Background(), sess, decls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, decls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var columns int
	if err := conn.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'contacts'`, schemaName).Scan(&columns); err != nil {
		t.Fatalf("count contacts columns: %v", err)
	}
	if columns != 2 {
		t.Errorf("contacts table has %d columns after the sync, want the other module's 2 untouched", columns)
	}
}

func TestToAtlasSchema_CrossModuleMany2One_UnloadedHardDependencyToleratedAsPlainColumn(t *testing.T) {
	s, err := ToAtlasSchema("tenant_acme", "sales", crossModuleOrderDecls(model.Many2One("contacts.contact")), nil,
		WithDependencies([]string{"contacts"}, nil), WithUnloadedDependenciesTolerated())
	if err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}
	order, _ := s.Table("order")
	if len(order.ForeignKeys) != 0 {
		t.Fatalf("got %d foreign keys, want none for an unloaded dependency", len(order.ForeignKeys))
	}
}

func TestToAtlasSchema_CrossModuleMany2One_DeclaringModelWithNoTable(t *testing.T) {
	decls := []model.ModelDeclaration{
		*model.Define("order", model.Virtual()).
			Field("id", model.UUID().Required().PrimaryKey()).
			Field("customer_id", model.Many2One("contacts.contact")),
	}
	opts := []AtlasOption{
		WithDependencies([]string{"contacts"}, nil),
		WithDependencyModels(map[string][]model.ModelDeclaration{"contacts": contactsDecls()}),
	}
	if _, err := ToAtlasSchema("tenant_acme", "sales", decls, nil, opts...); err != nil {
		t.Fatalf("ToAtlasSchema() error: %v", err)
	}

	if _, err := ToAtlasSchema("tenant_acme", "sales", decls, nil, WithDependencies([]string{"billing"}, nil)); err == nil {
		t.Error("expected the declared-dependency rule to apply to a model with no table too")
	}
}
