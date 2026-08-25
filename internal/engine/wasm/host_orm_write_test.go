package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// hostORMWriteCallerModule exports call_create/call_write/call_unlink,
// the same buildHostCallerModule forwarding-wrapper convention
// hostORMCallerModule (host_orm_test.go) uses for the read half.
var hostORMWriteCallerModule = buildHostCallerModule("host.orm", []string{"create", "write", "unlink"})

func newHostORMWriteCaller(t *testing.T, ctx context.Context, r *Runtime, mc *ModuleContext) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, hostORMWriteCallerModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("orm-write-caller-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

// newORMWriteTestModuleContext uses tenantSlug as both TenantSlug and
// TenantID — TenantID only needs to be a unique string per test here
// (it's stored as a JSONB string in river_job.args, not a real Postgres
// column), and reusing the already-unique slug keeps each test's
// EventDelivery job count isolated from every other test sharing the
// same primaryDB/river_job table.
func newORMWriteTestModuleContext(tenantSlug string, modelDecls []model.ModelDeclaration) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, tenantSlug, tenantSlug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: modelDecls})
}

// itemModelDecl is WithStandardFields()-shaped (soft-delete, etag) plus a
// required "name", a unique "code", and a Sequence "number" field —
// enough surface for create/write/unlink's core validation paths.
func itemModelDecl() model.ModelDeclaration {
	d := model.Define("item").WithStandardFields().
		Field("name", model.Text().Required()).
		Field("code", model.Text()).
		Field("number", model.Sequence("{year}")).
		Index("idx_items_code_unique", model.BTreeIndex("code").Unique())
	return *d
}

// hardDeleteItemModelDecl has no deleted_at field — unlink must hard
// DELETE it rather than soft-delete.
func hardDeleteItemModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "hard_item",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text().Required()},
		},
	}
}

func createFixtureItemsTable(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.item (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		created_by UUID,
		etag TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		code TEXT,
		number BIGINT
	)`); err != nil {
		t.Fatalf("create item table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE UNIQUE INDEX idx_items_code_unique ON `+schemaName+`.item (code)`); err != nil {
		t.Fatalf("create unique index: %v", err)
	}

	// The item model declares a Sequence field, so AcquireNext
	// (goerp#340) needs the sequences table this schema doesn't
	// otherwise get outside real tenant provisioning.
	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.sequences (
		model       TEXT NOT NULL,
		field       TEXT NOT NULL,
		period_key  TEXT NOT NULL,
		next_value  BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (model, field, period_key)
	)`); err != nil {
		t.Fatalf("create sequences table: %v", err)
	}
}

func createFixtureHardItemsTable(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.hard_item (
		id UUID PRIMARY KEY,
		name TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create hard_item table: %v", err)
	}
}

func countEventDeliveryJobsByName(t *testing.T, conn *sql.DB, eventName, tenantID string) int {
	t.Helper()
	var count int
	err := conn.QueryRow(
		`SELECT count(*) FROM river_job WHERE kind = 'event_delivery' AND args->>'event_name' = $1 AND args->>'tenant_id' = $2`,
		eventName, tenantID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count river_job rows: %v", err)
	}
	return count
}

func TestHostORM_Create_Succeeds_AcquiresSequence_EmitsEvent(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreatetest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	var out ORMCreateOutput
	env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget A"},
	}, &out)
	if !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	if out.Record["name"] != "Widget A" {
		t.Errorf("Record[name] = %v, want Widget A", out.Record["name"])
	}
	if out.Record["number"] == nil {
		t.Error("expected a Sequence value to have been acquired for number")
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", slug); got != 1 {
		t.Errorf("orm.record.created jobs = %d, want 1", got)
	}
}

func TestHostORM_Create_MissingRequiredField_ValidationFailed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreaterequiredtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001"},
	}, nil)
	if env.OK {
		t.Fatal("expected create to fail on a missing required field")
	}
	if env.Error.Code != abi.ErrCodeValidationFailed {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeValidationFailed)
	}
	if env.Error.Details["field"] != "name" {
		t.Errorf("Error.Details[field] = %v, want name", env.Error.Details["field"])
	}
}

func TestHostORM_Create_UniqueViolation(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreateuniquetest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	first := ORMCreateInput{Model: "testmodule.item", Record: map[string]any{
		"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A", "code": "DUP",
	}}
	if env := callORMHost(t, ctx, inst, "call_create", first, nil); !env.OK {
		t.Fatalf("first create failed: %+v", env.Error)
	}

	second := ORMCreateInput{Model: "testmodule.item", Record: map[string]any{
		"id": "22222222-2222-2222-2222-222222222222", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "B", "code": "DUP",
	}}
	env := callORMHost(t, ctx, inst, "call_create", second, nil)
	if env.OK {
		t.Fatal("expected a unique violation on duplicate code")
	}
	if env.Error.Code != abi.ErrCodeUniqueViolation {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeUniqueViolation)
	}
	if env.Error.Details["index"] != "idx_items_code_unique" {
		t.Errorf("Error.Details[index] = %v, want idx_items_code_unique", env.Error.Details["index"])
	}
}

func TestHostORM_Write_CorrectEtag_SucceedsAndRotatesEtag(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwriteetagtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	var created ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	originalEtag := created.Record["etag"].(string)

	var out ORMWriteOutput
	env := callORMHost(t, ctx, inst, "call_write", ORMWriteInput{
		Model: "testmodule.item", ID: id, Record: map[string]any{"name": "A renamed"}, ExpectedEtag: originalEtag,
	}, &out)
	if !env.OK {
		t.Fatalf("write failed: %+v", env.Error)
	}
	if out.Record["name"] != "A renamed" {
		t.Errorf("Record[name] = %v, want %q", out.Record["name"], "A renamed")
	}
	if out.Record["etag"] == originalEtag {
		t.Error("expected etag to rotate on a successful write")
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", slug); got != 1 {
		t.Errorf("orm.record.updated jobs = %d, want 1", got)
	}
}

func TestHostORM_Write_StaleEtag_EtagMismatch(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritestaletest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A"},
	}, nil); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}

	env := callORMHost(t, ctx, inst, "call_write", ORMWriteInput{
		Model: "testmodule.item", ID: id, Record: map[string]any{"name": "A renamed"}, ExpectedEtag: "stale-etag",
	}, nil)
	if env.OK {
		t.Fatal("expected a stale etag to fail")
	}
	if env.Error.Code != abi.ErrCodeEtagMismatch {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeEtagMismatch)
	}
}

func TestHostORM_Write_MissingRecord_NotFound(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritemissingtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_write", ORMWriteInput{
		Model: "testmodule.item", ID: "99999999-9999-9999-9999-999999999999", Record: map[string]any{"name": "X"}, ExpectedEtag: "whatever",
	}, nil)
	if env.OK {
		t.Fatal("expected a missing record to fail")
	}
	if env.Error.Code != abi.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeNotFound)
	}
}

func TestHostORM_Unlink_SoftDeletesWithStandardFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormunlinksofttest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A"},
	}, nil); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}

	var out ORMUnlinkOutput
	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{Model: "testmodule.item", ID: id}, &out)
	if !env.OK {
		t.Fatalf("unlink failed: %+v", env.Error)
	}
	if !out.Deleted {
		t.Error("expected Deleted = true")
	}

	var deletedAt sql.NullTime
	if err := primaryDB.QueryRow(`SELECT deleted_at FROM tenant_`+slug+`.item WHERE id = $1`, id).Scan(&deletedAt); err != nil {
		t.Fatalf("query row directly: %v", err)
	}
	if !deletedAt.Valid {
		t.Error("expected deleted_at to be set (soft delete), row was hard-deleted or untouched")
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.deleted", slug); got != 1 {
		t.Errorf("orm.record.deleted jobs = %d, want 1", got)
	}
}

func TestHostORM_Unlink_HardDeletesWithoutStandardFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormunlinkhardtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	if _, err := primaryDB.Exec(`INSERT INTO tenant_`+slug+`.hard_item (id, name) VALUES ($1, 'A')`, id); err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}

	var out ORMUnlinkOutput
	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{Model: "testmodule.hard_item", ID: id}, &out)
	if !env.OK {
		t.Fatalf("unlink failed: %+v", env.Error)
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_`+slug+`.hard_item WHERE id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Error("expected the row to be hard-deleted")
	}
}

func TestHostORM_Unlink_MissingRecord_NotFound(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormunlinkmissingtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{Model: "testmodule.item", ID: "99999999-9999-9999-9999-999999999999"}, nil)
	if env.OK {
		t.Fatal("expected unlink of a missing record to fail")
	}
	if env.Error.Code != abi.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeNotFound)
	}
}

func TestHostORM_Unlink_ForeignKeyViolation(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormunlinkfktest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	schemaName := "tenant_" + slug
	if _, err := primaryDB.Exec(`CREATE TABLE ` + schemaName + `.hard_item_child (
		id UUID PRIMARY KEY,
		parent_id UUID NOT NULL REFERENCES ` + schemaName + `.hard_item(id) ON DELETE RESTRICT
	)`); err != nil {
		t.Fatalf("create child table: %v", err)
	}

	parentID := "11111111-1111-1111-1111-111111111111"
	if _, err := primaryDB.Exec(`INSERT INTO `+schemaName+`.hard_item (id, name) VALUES ($1, 'A')`, parentID); err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	if _, err := primaryDB.Exec(`INSERT INTO `+schemaName+`.hard_item_child (id, parent_id) VALUES ($1, $2)`,
		"22222222-2222-2222-2222-222222222222", parentID); err != nil {
		t.Fatalf("insert child: %v", err)
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{Model: "testmodule.hard_item", ID: parentID}, nil)
	if env.OK {
		t.Fatal("expected a Restrict FK violation to fail")
	}
	if env.Error.Code != abi.ErrCodeForeignKeyViolation {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeForeignKeyViolation)
	}
}
