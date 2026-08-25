package wasm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func contactAndOrderModels() []model.ModelDeclaration {
	return []model.ModelDeclaration{
		{
			Name:  "contact",
			Table: "contacts",
			Fields: []model.NamedField{
				{Name: "id", Def: model.UUID().Required().PrimaryKey()},
				{Name: "display_name", Def: model.Text().Required()},
			},
		},
		{
			Name:  "order",
			Table: "orders",
			Fields: []model.NamedField{
				{Name: "id", Def: model.UUID().Required().PrimaryKey()},
				{Name: "customer_id", Def: model.Many2One("testmodule.contact")},
			},
		},
	}
}

func TestHostORM_SearchRead_ExpandsMany2One(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormrelexpandtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	schemaName := "tenant_" + slug

	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.contacts (
		id UUID PRIMARY KEY,
		display_name TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create contacts table: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.orders (
		id UUID PRIMARY KEY,
		customer_id UUID
	)`); err != nil {
		t.Fatalf("create orders table: %v", err)
	}

	customerID := "22222222-2222-2222-2222-222222222222"
	orderID := "33333333-3333-3333-3333-333333333333"
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".contacts (id, display_name) VALUES ($1, 'Acme Corp')", customerID); err != nil {
		t.Fatalf("insert contact: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".orders (id, customer_id) VALUES ($1, $2)", orderID, customerID); err != nil {
		t.Fatalf("insert order: %v", err)
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, contactAndOrderModels())
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.order"}, &out)
	if !env.OK {
		t.Fatalf("search_read failed: %+v", env.Error)
	}
	if len(out.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(out.Records))
	}
	rec := out.Records[0]

	if _, ok := rec["customer_id"]; !ok {
		t.Fatalf("expected customer_id to remain present, got %+v", rec)
	}

	customer, ok := rec["customer"].(map[string]any)
	if !ok {
		t.Fatalf("expected an expanded customer object, got %T: %+v", rec["customer"], rec["customer"])
	}
	if name, _ := customer["display_name"].(string); name != "Acme Corp" {
		t.Fatalf("customer.display_name = %v, want Acme Corp", customer["display_name"])
	}
	if id, _ := customer["id"].(string); id == "" {
		t.Fatalf("customer.id missing, got %+v", customer)
	}
}

func TestHostORM_SearchRead_ExpandsMany2One_NilFK(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormrelnilfktest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	schemaName := "tenant_" + slug

	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.contacts (
		id UUID PRIMARY KEY,
		display_name TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create contacts table: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.orders (
		id UUID PRIMARY KEY,
		customer_id UUID
	)`); err != nil {
		t.Fatalf("create orders table: %v", err)
	}

	orderID := "33333333-3333-3333-3333-333333333333"
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".orders (id, customer_id) VALUES ($1, NULL)", orderID); err != nil {
		t.Fatalf("insert order: %v", err)
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, contactAndOrderModels())
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.order"}, &out)
	if !env.OK {
		t.Fatalf("search_read failed: %+v", env.Error)
	}
	rec := out.Records[0]
	if v, ok := rec["customer"]; !ok || v != nil {
		t.Fatalf("expected customer to be present and nil for a nil FK, got %+v (present=%v)", v, ok)
	}
}

func TestHostORM_SearchRead_ExpandsMany2One_NoDisplayNameField(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormrelnodisplaytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	schemaName := "tenant_" + slug

	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.contacts (id UUID PRIMARY KEY)`); err != nil {
		t.Fatalf("create contacts table: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.orders (id UUID PRIMARY KEY, customer_id UUID)`); err != nil {
		t.Fatalf("create orders table: %v", err)
	}

	customerID := "22222222-2222-2222-2222-222222222222"
	orderID := "33333333-3333-3333-3333-333333333333"
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".contacts (id) VALUES ($1)", customerID); err != nil {
		t.Fatalf("insert contact: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".orders (id, customer_id) VALUES ($1, $2)", orderID, customerID); err != nil {
		t.Fatalf("insert order: %v", err)
	}

	modelDecls := []model.ModelDeclaration{
		{Name: "contact", Table: "contacts", Fields: []model.NamedField{{Name: "id", Def: model.UUID().Required().PrimaryKey()}}},
		{Name: "order", Table: "orders", Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "customer_id", Def: model.Many2One("testmodule.contact")},
		}},
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, modelDecls)
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.order"}, &out)
	if !env.OK {
		t.Fatalf("search_read failed: %+v", env.Error)
	}
	customer, ok := out.Records[0]["customer"].(map[string]any)
	if !ok {
		t.Fatalf("expected an expanded customer object even with no display_name field, got %+v", out.Records[0]["customer"])
	}
	if _, hasDisplayName := customer["display_name"]; hasDisplayName {
		t.Fatalf("expected no display_name key when the target model doesn't declare one, got %+v", customer)
	}
	if _, hasID := customer["id"]; !hasID {
		t.Fatalf("expected id to still be present, got %+v", customer)
	}
}

func TestHostORM_Read_ExpandsMany2One(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormrelreadtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	schemaName := "tenant_" + slug

	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.contacts (id UUID PRIMARY KEY, display_name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create contacts table: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.orders (id UUID PRIMARY KEY, customer_id UUID)`); err != nil {
		t.Fatalf("create orders table: %v", err)
	}

	customerID := "22222222-2222-2222-2222-222222222222"
	orderID := "33333333-3333-3333-3333-333333333333"
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".contacts (id, display_name) VALUES ($1, 'Acme Corp')", customerID); err != nil {
		t.Fatalf("insert contact: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".orders (id, customer_id) VALUES ($1, $2)", orderID, customerID); err != nil {
		t.Fatalf("insert order: %v", err)
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, contactAndOrderModels())
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMReadOutput
	env := callORMHost(t, ctx, inst, "call_read", ORMReadInput{Model: "testmodule.order", IDs: []string{orderID}}, &out)
	if !env.OK {
		t.Fatalf("read failed: %+v", env.Error)
	}
	customer, ok := out.Records[0]["customer"].(map[string]any)
	if !ok {
		t.Fatalf("expected an expanded customer object, got %+v", out.Records[0]["customer"])
	}
	if name, _ := customer["display_name"].(string); name != "Acme Corp" {
		t.Fatalf("customer.display_name = %v, want Acme Corp", customer["display_name"])
	}
}

func TestHostORM_SearchRead_ExpandsMany2One_MultipleRowsShareTarget(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormrelsharedtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	schemaName := "tenant_" + slug

	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.contacts (id UUID PRIMARY KEY, display_name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create contacts table: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.orders (id UUID PRIMARY KEY, customer_id UUID)`); err != nil {
		t.Fatalf("create orders table: %v", err)
	}

	customerID := "22222222-2222-2222-2222-222222222222"
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".contacts (id, display_name) VALUES ($1, 'Acme Corp')", customerID); err != nil {
		t.Fatalf("insert contact: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx,
		"INSERT INTO "+schemaName+".orders (id, customer_id) VALUES (gen_random_uuid(), $1), (gen_random_uuid(), $1)",
		customerID,
	); err != nil {
		t.Fatalf("insert orders: %v", err)
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, contactAndOrderModels())
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.order"}, &out)
	if !env.OK {
		t.Fatalf("search_read failed: %+v", env.Error)
	}
	if len(out.Records) != 2 {
		t.Fatalf("got %d records, want 2", len(out.Records))
	}
	for _, rec := range out.Records {
		customer, ok := rec["customer"].(map[string]any)
		if !ok {
			t.Fatalf("expected an expanded customer object on every row, got %+v", rec)
		}
		if name, _ := customer["display_name"].(string); name != "Acme Corp" {
			t.Fatalf("customer.display_name = %v, want Acme Corp", customer["display_name"])
		}
	}
}

// TestHostORM_SearchRead_RelationExpansionRespectsRLS confirms the related
// lookup runs in the same tenant-scoped transaction as the primary query,
// so an RLS policy on the target table excludes a related row exactly the
// way it excludes a directly-queried one — fail-closed, not an error.
func TestHostORM_SearchRead_RelationExpansionRespectsRLS(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormrelrlstest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	schemaName := "tenant_" + slug

	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.contacts (id UUID PRIMARY KEY, display_name TEXT NOT NULL, owner_contact_id UUID)`); err != nil {
		t.Fatalf("create contacts table: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.orders (id UUID PRIMARY KEY, customer_id UUID)`); err != nil {
		t.Fatalf("create orders table: %v", err)
	}

	viewerID := "44444444-4444-4444-4444-444444444444"
	customerID := "22222222-2222-2222-2222-222222222222"
	orderID := "33333333-3333-3333-3333-333333333333"
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".contacts (id, display_name, owner_contact_id) VALUES ($1, 'Acme Corp', $2)", customerID, viewerID); err != nil {
		t.Fatalf("insert contact: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO "+schemaName+".orders (id, customer_id) VALUES ($1, $2)", orderID, customerID); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, "ALTER TABLE "+schemaName+".contacts ENABLE ROW LEVEL SECURITY"); err != nil {
		t.Fatalf("enable RLS: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, `CREATE POLICY contacts_own_only ON `+schemaName+`.contacts
		FOR SELECT USING (owner_contact_id = current_setting('app.current_user_contact_id', true)::uuid)`); err != nil {
		t.Fatalf("create RLS policy: %v", err)
	}

	readerDB := openTestRLSReaderORM(t, primaryDB, schemaName, schemaName+".contacts")
	if _, err := primaryDB.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+schemaName+" TO goerp_test_rls_reader_orm"); err != nil {
		t.Fatalf("grant schema usage: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, "GRANT SELECT ON "+schemaName+".orders TO goerp_test_rls_reader_orm"); err != nil {
		t.Fatalf("grant select on orders: %v", err)
	}

	r := newHostDBTestRuntime(t, readerDB, 10)
	// ContactID does NOT match the contact's owner_contact_id — RLS should
	// exclude the related contacts row entirely.
	mc := NewModuleContext("req-1", "testmodule", "user-1", "55555555-5555-5555-5555-555555555555", []string{"admin"}, "tenant-id-1", slug, "trace-1", abi.CapDBRead, nil, ModuleSnapshot{ModelDecls: contactAndOrderModels()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.order"}, &out)
	if !env.OK {
		t.Fatalf("search_read failed: %+v", env.Error)
	}
	if len(out.Records) != 1 {
		t.Fatalf("got %d order records, want 1 (RLS shouldn't affect the primary query)", len(out.Records))
	}
	if v, ok := out.Records[0]["customer"]; !ok || v != nil {
		t.Fatalf("expected customer to be present and nil (RLS-excluded), got %+v (present=%v)", v, ok)
	}
}
