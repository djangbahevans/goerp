package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

const delegateSecret = "contacts:contact:secret"

type delegateFixture struct {
	db      *sql.DB
	runtime *Runtime
	mc      *ModuleContext
}

func newDelegateFixture(t *testing.T) *delegateFixture {
	t.Helper()
	conn := openTestPrimaryDB(t)
	slug := fmt.Sprintf("delegate%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, conn, slug)
	schema := "tenant_" + slug
	for _, stmt := range []string{
		`CREATE TABLE ` + schema + `.contacts (id UUID PRIMARY KEY DEFAULT uuidv7(), name TEXT NOT NULL CHECK (name <> 'invalid'), phone TEXT, deleted_at TIMESTAMPTZ)`,
		`CREATE TABLE ` + schema + `.employees (id UUID PRIMARY KEY DEFAULT uuidv7(), contact_id UUID NOT NULL REFERENCES ` + schema + `.contacts(id), department TEXT NOT NULL CHECK (department <> 'invalid'), etag TEXT DEFAULT '', deleted_at TIMESTAMPTZ)`,
	} {
		if _, err := conn.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	contact := model.ModelDeclaration{
		Name:  "contact",
		Table: "contacts",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().PrimaryKey().Readonly().Default("uuidv7()")},
			{Name: "name", Def: model.Text().Required()},
			{Name: "phone", Def: model.Text().Access(model.AccessRead(perm.Ref(delegateSecret)), model.AccessWrite(perm.Ref(delegateSecret))).OnDeniedRead(model.Omit)},
			{Name: "deleted_at", Def: model.TimestampTZ()},
		},
	}
	employee := model.ModelDeclaration{
		Name:  "employee",
		Table: "employees",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().PrimaryKey().Readonly().Default("uuidv7()")},
			{Name: "contact_id", Def: model.Delegate("contacts.contact").Required()},
			{Name: "department", Def: model.Text().Required()},
			{Name: "etag", Def: model.Text().Readonly().Default("''")},
			{Name: "deleted_at", Def: model.TimestampTZ()},
		},
	}
	fields := fieldsec.New()
	fields.Register("hr", []model.ModelDeclaration{employee})
	fields.Register("contacts", []model.ModelDeclaration{contact})
	permissions := permission.NewPermissionRegistry()
	permissions.Register("contacts", []manifest.Permission{{Name: delegateSecret}})
	tenantID := uuid.New().String()
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.river_job WHERE args->>'tenant_id' = $1", tenantID)
	})
	mc := NewModuleContext("req", "hr", "user", "contact", nil, nil, tenantID, slug, "trace", abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
		ModelDecls:         []model.ModelDeclaration{employee},
		ComputeTargets:     map[string]ComputeTarget{"contacts": {ModelDecls: []model.ModelDeclaration{contact}}},
		FieldSecRegistry:   fields,
		PermissionRegistry: permissions,
	})
	return &delegateFixture{db: conn, runtime: newHostDBTestRuntime(t, conn, 10), mc: mc}
}

func (f *delegateFixture) create(t *testing.T, values map[string]any) map[string]any {
	t.Helper()
	out, hostErr := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{Model: "hr.employee", Record: values})
	if hostErr != nil {
		t.Fatalf("create: %+v", hostErr)
	}
	return out.Record
}

func TestORMDelegateCreateReadWrite(t *testing.T) {
	f := newDelegateFixture(t)
	row := f.create(t, map[string]any{"name": "Kwame", "department": "Sales"})
	id := row["id"].(string)
	if row["name"] != "Kwame" || row["contact_id"] == nil || row["id"] == row["contact_id"] {
		t.Fatalf("composed create lost separate identities: %+v", row)
	}

	for _, fields := range [][]string{nil, {"name"}, {"id", "name", "department"}} {
		out, hostErr := ORMRead(t.Context(), f.db, nil, f.mc, abiv1.ORMReadInput{Model: "hr.employee", IDs: []string{id}, Fields: fields})
		if hostErr != nil || len(out.Records) != 1 || out.Records[0]["name"] != "Kwame" {
			t.Fatalf("read %v: %+v, %+v", fields, out, hostErr)
		}
		if len(fields) > 0 && len(out.Records[0]) != len(fields) {
			t.Fatalf("projection leaked internal fields: %+v", out)
		}
		page, hostErr := ORMSearchRead(t.Context(), f.db, f.mc, abiv1.ORMSearchReadInput{Model: "hr.employee", Fields: fields, Limit: 1})
		if hostErr != nil || len(page.Records) != 1 || page.Records[0]["name"] != "Kwame" || page.NextCursor != id {
			t.Fatalf("search_read %v: %+v, %+v", fields, page, hostErr)
		}
	}

	out, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "hr.employee", ID: id, Record: map[string]any{"name": "Ama", "department": "Support"}})
	if hostErr != nil || out.Record["name"] != "Ama" || out.Record["department"] != "Support" {
		t.Fatalf("split write: %+v, %+v", out, hostErr)
	}

	out, hostErr = ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "hr.employee", ID: id, Record: map[string]any{"name": "Kofi"}})
	if hostErr != nil || out.Record["name"] != "Kofi" || out.Record["department"] != "Support" {
		t.Fatalf("delegate-only write: %+v, %+v", out, hostErr)
	}
	linked := f.create(t, map[string]any{"contact_id": row["contact_id"], "department": "Finance"})
	if linked["contact_id"] != row["contact_id"] || linked["name"] != "Kofi" {
		t.Fatalf("link existing: %+v", linked)
	}
	var count int
	if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tenant_"+f.mc.TenantSlug+".contacts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("link created another target: %d, %v", count, err)
	}
}

func TestORMDelegateRollback(t *testing.T) {
	f := newDelegateFixture(t)
	for _, values := range []map[string]any{
		{"name": "Kwame", "department": "invalid"},
		{"name": "invalid", "department": "Sales"},
		{"name": "Kwame"},
	} {
		if _, hostErr := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{Model: "hr.employee", Record: values}); hostErr == nil {
			t.Fatalf("invalid create succeeded: %+v", values)
		}
		for _, table := range []string{"contacts", "employees"} {
			var count int
			if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tenant_"+f.mc.TenantSlug+"."+table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("create did not roll back %s: %d, %v", table, count, err)
			}
		}
	}

	row := f.create(t, map[string]any{"name": "Kwame", "department": "Sales"})
	for _, values := range []map[string]any{
		{"name": "Ama", "department": "invalid"},
		{"name": "invalid", "department": "Support"},
		{"phone": "secret", "department": "Support"},
	} {
		if _, hostErr := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "hr.employee", ID: row["id"].(string), Record: values}); hostErr == nil {
			t.Fatalf("invalid write succeeded: %+v", values)
		}
		var name, department string
		if err := f.db.QueryRowContext(t.Context(), "SELECT c.name, e.department FROM tenant_"+f.mc.TenantSlug+".employees e JOIN tenant_"+f.mc.TenantSlug+".contacts c ON c.id = e.contact_id").Scan(&name, &department); err != nil || name != "Kwame" || department != "Sales" {
			t.Fatalf("write did not roll back both rows: %s, %s, %v", name, department, err)
		}
	}
}

func TestORMDelegateFieldSecurityAndRLS(t *testing.T) {
	f := newDelegateFixture(t)
	index, _ := f.mc.PermissionRegistry().Index(delegateSecret)
	f.mc.PermissionSet.Set(index)
	row := f.create(t, map[string]any{"name": "Kwame", "phone": "0241234567", "department": "Sales"})
	f.mc.PermissionSet = nil
	id := row["id"].(string)
	read, hostErr := ORMRead(t.Context(), f.db, nil, f.mc, abiv1.ORMReadInput{Model: "hr.employee", IDs: []string{id}})
	if hostErr != nil || len(read.Records) != 1 || read.Records[0]["name"] != "Kwame" {
		t.Fatalf("read: %+v, %+v", read, hostErr)
	}
	if _, present := read.Records[0]["phone"]; present {
		t.Fatalf("delegated field security bypassed: %+v", read)
	}

	schema := "tenant_" + f.mc.TenantSlug
	for _, stmt := range []string{
		"ALTER TABLE " + schema + ".contacts ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY hidden ON " + schema + ".contacts FOR SELECT USING (false)",
	} {
		if _, err := f.db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	reader := openTestRLSReaderORM(t, f.db, schema, schema+".contacts")
	if _, err := f.db.ExecContext(t.Context(), "GRANT SELECT ON "+schema+".employees TO goerp_test_rls_reader_orm"); err != nil {
		t.Fatal(err)
	}
	read, hostErr = ORMRead(t.Context(), reader, nil, f.mc, abiv1.ORMReadInput{Model: "hr.employee", IDs: []string{id}})
	if hostErr != nil || len(read.Records) != 1 || read.Records[0]["department"] != "Sales" {
		t.Fatalf("RLS read: %+v, %+v", read, hostErr)
	}
	if _, present := read.Records[0]["name"]; present {
		t.Fatalf("RLS-hidden target leaked: %+v", read)
	}
}

func TestORMDelegateBorrowedTransactionRollback(t *testing.T) {
	f := newDelegateFixture(t)
	tx := registerTenantScopedTestTx(t, t.Context(), f.db, f.mc, "delegate-tx")
	defer func() { _ = tx.Rollback() }()
	_, hostErr := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{Model: "hr.employee", TxID: "delegate-tx", Record: map[string]any{"name": "Kwame", "department": "invalid"}})
	if hostErr == nil {
		t.Fatal("invalid create succeeded")
	}
	var count int
	if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM contacts").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed create left a target or aborted borrowed transaction: %d, %v", count, err)
	}
	out, hostErr := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{Model: "hr.employee", TxID: "delegate-tx", Record: map[string]any{"name": "Kwame", "department": "Sales"}})
	if hostErr != nil || out.Record["name"] != "Kwame" {
		t.Fatalf("borrowed create: %+v, %+v", out, hostErr)
	}
	if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tenant_"+f.mc.TenantSlug+".employees").Scan(&count); err != nil || count != 0 {
		t.Fatalf("borrowed transaction committed: %d, %v", count, err)
	}
}

func TestORMDelegateBulkAndBorrowedWrite(t *testing.T) {
	f := newDelegateFixture(t)
	batch, hostErr := ORMCreateBatch(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), f.mc, abiv1.ORMCreateBatchInput{Model: "hr.employee", Records: []map[string]any{
		{"name": "Kwame", "department": "Sales"},
		{"name": "Ama", "department": "Support"},
	}})
	if hostErr != nil || len(batch.Records) != 2 || batch.Records[1]["name"] != "Ama" {
		t.Fatalf("create batch: %+v, %+v", batch, hostErr)
	}
	ids := []string{batch.Records[0]["id"].(string), batch.Records[1]["id"].(string)}
	written, hostErr := ORMWriteMany(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), f.mc, abiv1.ORMWriteManyInput{Model: "hr.employee", IDs: ids, Record: map[string]any{"name": "Kofi", "department": "Finance"}})
	if hostErr != nil || written.Count != 2 {
		t.Fatalf("write many: %+v, %+v", written, hostErr)
	}

	tx := registerTenantScopedTestTx(t, t.Context(), f.db, f.mc, "delegate-tx")
	defer func() { _ = tx.Rollback() }()
	_, hostErr = ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "hr.employee", ID: ids[0], TxID: "delegate-tx", Record: map[string]any{"name": "invalid", "department": "Support"}})
	if hostErr == nil {
		t.Fatal("invalid target write succeeded")
	}
	read, hostErr := ORMRead(t.Context(), f.db, nil, f.mc, abiv1.ORMReadInput{Model: "hr.employee", IDs: ids, TxID: "delegate-tx"})
	if hostErr != nil || len(read.Records) != 2 {
		t.Fatalf("read after failed borrowed write: %+v, %+v", read, hostErr)
	}
	for _, record := range read.Records {
		if record["name"] != "Kofi" || record["department"] != "Finance" {
			t.Fatalf("failed write changed a row: %+v", record)
		}
	}
}

func TestHostORMDelegate(t *testing.T) {
	f := newDelegateFixture(t)
	inst := newHostORMWriteCaller(t, t.Context(), f.runtime, f.mc)
	var created abiv1.ORMCreateOutput
	env := callORMHost(t, t.Context(), inst, "call_create", abiv1.ORMCreateInput{Model: "hr.employee", Record: map[string]any{"name": "Kwame", "department": "Sales"}}, &created)
	if !env.OK || created.Record["name"] != "Kwame" {
		t.Fatalf("WASM create: %+v, %+v", env, created)
	}
	var read abiv1.ORMReadOutput
	reader := newHostORMCaller(t, t.Context(), f.runtime, f.mc)
	env = callORMHost(t, t.Context(), reader, "call_read", abiv1.ORMReadInput{Model: "hr.employee", IDs: []string{created.Record["id"].(string)}, Fields: []string{"name"}}, &read)
	if !env.OK || len(read.Records) != 1 || read.Records[0]["name"] != "Kwame" {
		t.Fatalf("WASM read: %+v, %+v", env, read)
	}
}

func TestORMDelegateTargetWritePolicy(t *testing.T) {
	f := newDelegateFixture(t)
	row := f.create(t, map[string]any{"name": "Kwame", "department": "Sales"})
	schema := "tenant_" + f.mc.TenantSlug
	for _, stmt := range []string{
		"ALTER TABLE " + schema + ".contacts ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY visible ON " + schema + ".contacts FOR SELECT USING (true)",
		"CREATE POLICY deny_update ON " + schema + ".contacts FOR UPDATE USING (false)",
	} {
		if _, err := f.db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	writer := openTestRLSReaderORM(t, f.db, schema, schema+".contacts")
	if _, err := f.db.ExecContext(t.Context(), "GRANT SELECT, UPDATE ON "+schema+".employees, "+schema+".contacts TO goerp_test_rls_reader_orm"); err != nil {
		t.Fatal(err)
	}
	_, hostErr := ORMWrite(t.Context(), f.runtime, writer, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "hr.employee", ID: row["id"].(string), Record: map[string]any{"name": "Ama", "department": "Support"}})
	if hostErr == nil {
		t.Fatal("target UPDATE policy bypassed")
	}
	var name, department string
	if err := f.db.QueryRowContext(t.Context(), "SELECT c.name, e.department FROM "+schema+".employees e JOIN "+schema+".contacts c ON c.id = e.contact_id").Scan(&name, &department); err != nil || name != "Kwame" || department != "Sales" {
		t.Fatalf("target policy failure changed either row: %s, %s, %v", name, department, err)
	}
}

func TestORMDelegateUnavailableAndDeniedRelation(t *testing.T) {
	f := newDelegateFixture(t)
	row := f.create(t, map[string]any{"name": "Kwame", "department": "Sales"})
	md := &f.mc.snapshot.ModelDecls[0]
	md.Fields[1].Def = md.Fields[1].Def.Access(model.AccessRead(perm.Ref(delegateSecret)), model.AccessWrite(perm.Ref(delegateSecret))).OnDeniedRead(model.Mask(fmt.Sprint(row["contact_id"]))).OnDeniedWrite(model.Ignore)
	f.mc.FieldSecRegistry().Register("hr", f.mc.ModelDecls())
	read, hostErr := ORMRead(t.Context(), f.db, nil, f.mc, abiv1.ORMReadInput{Model: "hr.employee", IDs: []string{row["id"].(string)}, Fields: []string{"name"}})
	if hostErr != nil || len(read.Records) != 1 || len(read.Records[0]) != 0 {
		t.Fatalf("denied relation expanded: %+v, %+v", read, hostErr)
	}
	_, hostErr = ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMWriteInput{Model: "hr.employee", ID: row["id"].(string), Record: map[string]any{"name": "Ama", "department": "Support"}})
	if hostErr != nil {
		t.Fatalf("ignored relation write: %+v", hostErr)
	}
	var name string
	if err := f.db.QueryRowContext(t.Context(), "SELECT name FROM tenant_"+f.mc.TenantSlug+".contacts").Scan(&name); err != nil || name != "Kwame" {
		t.Fatalf("ignored delegated write reached target: %s, %v", name, err)
	}

	index, _ := f.mc.PermissionRegistry().Index(delegateSecret)
	f.mc.PermissionSet.Set(index)
	if _, err := f.db.ExecContext(t.Context(), "UPDATE tenant_"+f.mc.TenantSlug+".contacts SET deleted_at = NOW()"); err != nil {
		t.Fatal(err)
	}
	read, hostErr = ORMRead(t.Context(), f.db, nil, f.mc, abiv1.ORMReadInput{Model: "hr.employee", IDs: []string{row["id"].(string)}})
	if hostErr != nil || len(read.Records) != 1 {
		t.Fatalf("soft-deleted target read: %+v, %+v", read, hostErr)
	}
	if _, present := read.Records[0]["name"]; present {
		t.Fatalf("soft-deleted target expanded: %+v", read)
	}
	_, hostErr = ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.mc, abiv1.ORMCreateInput{Model: "hr.employee", Record: map[string]any{"contact_id": row["contact_id"], "department": "Sales"}})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeNotFound {
		t.Fatalf("soft-deleted target linked: %+v", hostErr)
	}
}
