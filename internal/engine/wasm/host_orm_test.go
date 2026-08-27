package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/vmihailenco/msgpack/v5"
)

// hostORMCallerModule is built once per test process by
// buildHostCallerModule (testwasm_test.go), importing host.orm.search/
// search_read/read and re-exporting each as call_search/call_search_read/
// call_read — the same forwarding-wrapper shape hostDBCallerModule and
// hostStorageCallerModule hand-assemble individually.
var hostORMCallerModule = buildHostCallerModule("host.orm", []string{"search", "search_read", "read"})

func newHostORMCaller(t *testing.T, ctx context.Context, r *Runtime, mc *ModuleContext) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, hostORMCallerModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("orm-caller-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

func widgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "widget",
		Table: "widgets",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text().Required()},
			{Name: "price", Def: model.Integer()},
		},
	}
}

func newORMTestModuleContext(tenantSlug string, modelDecls []model.ModelDeclaration) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, "tenant-id-1", tenantSlug, "trace-1", abi.CapDBRead, nil, ModuleSnapshot{ModelDecls: modelDecls})
}

// createFixtureWidgetsTable creates a plain, RLS-free widgets table (no
// SchemaDiffEngine involved — this test targets host.orm's own dispatch
// logic, not schema sync) and seeds it with rows.
func createFixtureWidgetsTable(t *testing.T, conn *sql.DB, slug string, rows [][2]string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.widgets (
		id UUID PRIMARY KEY,
		name TEXT NOT NULL,
		price INTEGER
	)`); err != nil {
		t.Fatalf("create widgets table: %v", err)
	}

	for _, row := range rows {
		if _, err := conn.ExecContext(ctx, "INSERT INTO "+schemaName+".widgets (id, name, price) VALUES ($1, $2, 100)", row[0], row[1]); err != nil {
			t.Fatalf("insert fixture row: %v", err)
		}
	}
}

func callORMHost(t *testing.T, ctx context.Context, inst *ModuleInstance, exportName string, req any, out any) wireEnvelope {
	t.Helper()
	env := callHost(t, ctx, inst, exportName, req)
	if env.OK && out != nil {
		if err := msgpack.Unmarshal(env.Data, out); err != nil {
			t.Fatalf("unmarshal %s output: %v", exportName, err)
		}
	}
	return env
}

func TestHostORM_Search_ReturnsIDsAndCount(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	id1, id2 := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	createFixtureWidgetsTable(t, primaryDB, slug, [][2]string{{id1, "Widget A"}, {id2, "Widget B"}})

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchOutput
	env := callORMHost(t, ctx, inst, "call_search", ORMSearchInput{Model: "testmodule.widget"}, &out)
	if !env.OK {
		t.Fatalf("search failed: %+v", env.Error)
	}
	if out.Count != 2 || len(out.IDs) != 2 {
		t.Fatalf("got %+v, want 2 ids and count 2", out)
	}
}

func TestHostORM_Search_DomainFilters(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchfiltertest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	id1, id2 := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	createFixtureWidgetsTable(t, primaryDB, slug, [][2]string{{id1, "Widget A"}, {id2, "Widget B"}})

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchOutput
	env := callORMHost(t, ctx, inst, "call_search", ORMSearchInput{Model: "testmodule.widget", Domain: "record.name = 'Widget A'"}, &out)
	if !env.OK {
		t.Fatalf("search failed: %+v", env.Error)
	}
	if out.Count != 1 || len(out.IDs) != 1 || out.IDs[0] != id1 {
		t.Fatalf("got %+v, want exactly [%s]", out, id1)
	}
}

func TestHostORM_Search_CountIgnoresLimitOffset(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchpagetest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	rows := [][2]string{
		{"11111111-1111-1111-1111-111111111111", "A"},
		{"22222222-2222-2222-2222-222222222222", "B"},
		{"33333333-3333-3333-3333-333333333333", "C"},
	}
	createFixtureWidgetsTable(t, primaryDB, slug, rows)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchOutput
	env := callORMHost(t, ctx, inst, "call_search", ORMSearchInput{Model: "testmodule.widget", Limit: 1}, &out)
	if !env.OK {
		t.Fatalf("search failed: %+v", env.Error)
	}
	if out.Count != 3 {
		t.Fatalf("count = %d, want 3 (ignoring limit)", out.Count)
	}
	if len(out.IDs) != 1 {
		t.Fatalf("ids = %v, want exactly 1 (respecting limit)", out.IDs)
	}
}

func TestHostORM_Search_UnknownModel(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchunknowntest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_search", ORMSearchInput{Model: "testmodule.nonexistent"}, nil)
	if env.OK {
		t.Fatal("expected an error for an unknown model")
	}
	if env.Error.Code != abi.ErrCodeModelNotFound {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeModelNotFound)
	}
}

func TestHostORM_Search_OtherModulesModelIsAlsoNotFound(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchcrossmoduletest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	// "widget" is declared, but under a different module prefix than
	// this caller's own ("testmodule") — a module can only address its
	// own models through host.orm.
	env := callORMHost(t, ctx, inst, "call_search", ORMSearchInput{Model: "othermodule.widget"}, nil)
	if env.OK {
		t.Fatal("expected an error resolving another module's model")
	}
	if env.Error.Code != abi.ErrCodeModelNotFound {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeModelNotFound)
	}
}

func TestHostORM_Search_InvalidDomain(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchbaddomaintest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureWidgetsTable(t, primaryDB, slug, nil)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_search", ORMSearchInput{Model: "testmodule.widget", Domain: "record.name ==="}, nil)
	if env.OK {
		t.Fatal("expected an error for an unparseable domain")
	}
	if env.Error.Code != abi.ErrCodeDomainInvalid {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeDomainInvalid)
	}
}

func TestHostORM_Search_CapabilityDenied(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchnocaptest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureWidgetsTable(t, primaryDB, slug, nil)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, "tenant-id-1", slug, "trace-1", abi.CapabilitySet(0), nil, ModuleSnapshot{ModelDecls: []model.ModelDeclaration{widgetModelDecl()}})
	inst := newHostORMCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_search", ORMSearchInput{Model: "testmodule.widget"}, nil)
	if env.OK {
		t.Fatal("expected an error without db.read capability")
	}
	if env.Error.Code != abi.ErrCodeCapabilityDenied {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
	}
}

func TestHostORM_SearchRead_ReturnsRequestedFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchreadtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	id1 := "11111111-1111-1111-1111-111111111111"
	createFixtureWidgetsTable(t, primaryDB, slug, [][2]string{{id1, "Widget A"}})

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.widget", Fields: []string{"id", "name"}}, &out)
	if !env.OK {
		t.Fatalf("search_read failed: %+v", env.Error)
	}
	if len(out.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(out.Records))
	}
	rec := out.Records[0]
	if _, ok := rec["price"]; ok {
		t.Fatalf("record included unrequested field price: %+v", rec)
	}
	if name, _ := rec["name"].(string); name != "Widget A" {
		t.Fatalf("name = %v, want Widget A", rec["name"])
	}
}

func TestHostORM_SearchRead_EmptyFieldsReturnsAll(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchreadalltest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	id1 := "11111111-1111-1111-1111-111111111111"
	createFixtureWidgetsTable(t, primaryDB, slug, [][2]string{{id1, "Widget A"}})

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.widget"}, &out)
	if !env.OK {
		t.Fatalf("search_read failed: %+v", env.Error)
	}
	rec := out.Records[0]
	for _, f := range []string{"id", "name", "price"} {
		if _, ok := rec[f]; !ok {
			t.Errorf("expected field %s to be present when Fields is empty, got %+v", f, rec)
		}
	}
}

func TestHostORM_SearchRead_UnknownFieldRejected(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormsearchreadbadfieldtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureWidgetsTable(t, primaryDB, slug, nil)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.widget", Fields: []string{"nonexistent_field"}}, nil)
	if env.OK {
		t.Fatal("expected an error for an unknown field")
	}
	if env.Error.Code != abi.ErrCodeFieldUnknown {
		t.Fatalf("error code = %q, want %q", env.Error.Code, abi.ErrCodeFieldUnknown)
	}
}

func TestHostORM_SearchRead_CursorPagination(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcursortest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	rows := [][2]string{
		{"11111111-1111-1111-1111-111111111111", "A"},
		{"22222222-2222-2222-2222-222222222222", "B"},
		{"33333333-3333-3333-3333-333333333333", "C"},
	}
	createFixtureWidgetsTable(t, primaryDB, slug, rows)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var page1 ORMSearchReadOutput
	env := callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.widget", Limit: 2}, &page1)
	if !env.OK {
		t.Fatalf("search_read page 1 failed: %+v", env.Error)
	}
	if len(page1.Records) != 2 || page1.NextCursor == "" {
		t.Fatalf("page 1 = %+v, want 2 records and a next_cursor", page1)
	}

	var page2 ORMSearchReadOutput
	env = callORMHost(t, ctx, inst, "call_search_read", ORMSearchReadInput{Model: "testmodule.widget", Limit: 2, Cursor: page1.NextCursor}, &page2)
	if !env.OK {
		t.Fatalf("search_read page 2 failed: %+v", env.Error)
	}
	if len(page2.Records) != 1 {
		t.Fatalf("page 2 = %+v, want the remaining 1 record", page2)
	}

	seen := map[string]bool{}
	for _, rec := range append(page1.Records, page2.Records...) {
		seen[fmt.Sprintf("%v", rec["id"])] = true
	}
	if len(seen) != 3 {
		t.Fatalf("paged through %d distinct ids across both pages, want 3", len(seen))
	}
}

func TestHostORM_Read_ByIDs(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormreadtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	id1, id2 := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	createFixtureWidgetsTable(t, primaryDB, slug, [][2]string{{id1, "Widget A"}, {id2, "Widget B"}})

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMReadOutput
	env := callORMHost(t, ctx, inst, "call_read", ORMReadInput{Model: "testmodule.widget", IDs: []string{id1}}, &out)
	if !env.OK {
		t.Fatalf("read failed: %+v", env.Error)
	}
	if len(out.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(out.Records))
	}
	if name, _ := out.Records[0]["name"].(string); name != "Widget A" {
		t.Fatalf("name = %v, want Widget A", out.Records[0]["name"])
	}
}

func TestHostORM_Read_EmptyIDsReturnsEmptyRecords(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormreademptytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureWidgetsTable(t, primaryDB, slug, nil)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMReadOutput
	env := callORMHost(t, ctx, inst, "call_read", ORMReadInput{Model: "testmodule.widget", IDs: nil}, &out)
	if !env.OK {
		t.Fatalf("read failed: %+v", env.Error)
	}
	if len(out.Records) != 0 {
		t.Fatalf("got %d records, want 0", len(out.Records))
	}
}

func TestHostORM_Read_MissingIDsAreSilentlyAbsent(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormreadmissingtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	id1 := "11111111-1111-1111-1111-111111111111"
	createFixtureWidgetsTable(t, primaryDB, slug, [][2]string{{id1, "Widget A"}})

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMTestModuleContext(slug, []model.ModelDeclaration{widgetModelDecl()})
	inst := newHostORMCaller(t, ctx, r, mc)

	missing := "99999999-9999-9999-9999-999999999999"
	var out ORMReadOutput
	env := callORMHost(t, ctx, inst, "call_read", ORMReadInput{Model: "testmodule.widget", IDs: []string{id1, missing}}, &out)
	if !env.OK {
		t.Fatalf("read failed: %+v", env.Error)
	}
	if len(out.Records) != 1 {
		t.Fatalf("got %d records, want 1 (missing id silently absent)", len(out.Records))
	}
}

// openTestRLSReaderORM creates (or reuses) a NOSUPERUSER, non-BYPASSRLS
// login role and returns a *sql.DB connected as it — the same convention
// internal/engine/schema/rls_test.go's openTestRLSReader uses, duplicated
// here since it's a schema-package-private test helper.
func openTestRLSReaderORM(t *testing.T, adminConn *sql.DB, schemaName, table string) *sql.DB {
	t.Helper()

	const roleName = "goerp_test_rls_reader_orm"
	if _, err := adminConn.Exec(`
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + roleName + `') THEN
				CREATE ROLE ` + roleName + ` LOGIN PASSWORD 'dev' NOSUPERUSER NOBYPASSRLS;
			END IF;
		END
		$$;
	`); err != nil {
		t.Fatalf("create test reader role: %v", err)
	}
	if _, err := adminConn.Exec("GRANT USAGE ON SCHEMA " + schemaName + " TO " + roleName); err != nil {
		t.Fatalf("grant schema usage: %v", err)
	}
	if _, err := adminConn.Exec("GRANT SELECT ON " + table + " TO " + roleName); err != nil {
		t.Fatalf("grant select: %v", err)
	}

	readerConn, err := db.New("postgres://" + roleName + ":dev@localhost:55432/goerp")
	if err != nil {
		t.Fatalf("connect as test reader role: %v", err)
	}
	t.Cleanup(func() { _ = readerConn.Close() })

	return readerConn
}

// TestHostORM_Search_RespectsRLS confirms host.orm does nothing extra for
// row filtering — the tenant-scoped session variables it sets are the same
// ones an RLS policy (goerp#71/#72) reads, so a policy attached to the
// table filters host.orm.search results automatically.
func TestHostORM_Search_RespectsRLS(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormrlstest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)

	repID := "22222222-2222-2222-2222-222222222222"
	otherID := "33333333-3333-3333-3333-333333333333"
	schemaName := "tenant_" + slug

	if _, err := primaryDB.ExecContext(ctx, `CREATE TABLE `+schemaName+`.widgets (
		id UUID PRIMARY KEY,
		name TEXT NOT NULL,
		salesperson_id UUID
	)`); err != nil {
		t.Fatalf("create widgets table: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx,
		"INSERT INTO "+schemaName+".widgets (id, name, salesperson_id) VALUES (gen_random_uuid(), 'mine', $1), (gen_random_uuid(), 'not mine', $2)",
		repID, otherID,
	); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, "ALTER TABLE "+schemaName+".widgets ENABLE ROW LEVEL SECURITY"); err != nil {
		t.Fatalf("enable RLS: %v", err)
	}
	if _, err := primaryDB.ExecContext(ctx, `CREATE POLICY widgets_own_only ON `+schemaName+`.widgets
		FOR SELECT USING (salesperson_id = current_setting('app.current_user_contact_id', true)::uuid)`); err != nil {
		t.Fatalf("create RLS policy: %v", err)
	}

	widgetModel := model.ModelDeclaration{
		Name:  "widget",
		Table: "widgets",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text().Required()},
			{Name: "salesperson_id", Def: model.UUID()},
		},
	}

	// The dev-stack's `goerp` role is a Postgres-image-created superuser,
	// and RLS is a no-op for a superuser connection regardless of session
	// vars (multitenancy-internals.md §5a) — same gotcha
	// internal/engine/schema/rls_test.go hit. host.orm's own runtime must
	// query through a genuinely restricted role for this test to mean
	// anything.
	readerDB := openTestRLSReaderORM(t, primaryDB, schemaName, schemaName+".widgets")

	r := newHostDBTestRuntime(t, readerDB, 10)
	// ContactID matches repID — the RLS policy should admit only the row
	// with salesperson_id = repID, even though no domain filter asks for it.
	mc := NewModuleContext("req-1", "testmodule", "user-1", repID, []string{"admin"}, nil, "tenant-id-1", slug, "trace-1", abi.CapDBRead, nil, ModuleSnapshot{ModelDecls: []model.ModelDeclaration{widgetModel}})
	inst := newHostORMCaller(t, ctx, r, mc)

	var out ORMSearchOutput
	env := callORMHost(t, ctx, inst, "call_search", ORMSearchInput{Model: "testmodule.widget"}, &out)
	if !env.OK {
		t.Fatalf("search failed: %+v", env.Error)
	}
	if out.Count != 1 {
		t.Fatalf("count = %d, want 1 (RLS should exclude the other salesperson's row)", out.Count)
	}
}
