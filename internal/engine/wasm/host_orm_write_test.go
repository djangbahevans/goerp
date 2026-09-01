package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// hostORMWriteCallerModule exports call_create/call_create_batch/
// call_first_or_create/call_write/call_write_many/call_write_where/
// call_unlink, the same buildHostCallerModule forwarding-wrapper
// convention hostORMCallerModule (host_orm_test.go) uses for the read
// half.
var hostORMWriteCallerModule = buildHostCallerModule("host.orm", []string{
	"create", "create_batch", "first_or_create", "write", "write_many", "write_where", "unlink",
})

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
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, tenantSlug, tenantSlug, "trace-1",
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

// readonlyFieldModelDecl has one Readonly() field (internal_ref) —
// buildAssignment must reject any caller-supplied value for it, the same
// as a Computed field.
func readonlyFieldModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "locked_item",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "name", Def: model.Text().Required()},
			{Name: "internal_ref", Def: model.Text().Readonly()},
		},
	}
}

func createFixtureLockedItemsTable(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.locked_item (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		name TEXT NOT NULL,
		internal_ref TEXT
	)`); err != nil {
		t.Fatalf("create locked_item table: %v", err)
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

// TestHostORM_Create_TxID_ParticipatesInCallersTransaction proves
// ORMCreate never commits or rolls back a borrowed transaction itself:
// the row and its orm.record.created event-delivery job must stay
// invisible to a separate connection (primaryDB itself, not tx) until
// the caller — not ORMCreate — actually commits.
func TestHostORM_Create_TxID_ParticipatesInCallersTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreatetxtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	insertClient := r.EventInsertClient()
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	tx, err := beginTenantScopedWrite(ctx, primaryDB, mc)
	if err != nil {
		t.Fatalf("beginTenantScopedWrite: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	const txID = "test-tx-1"
	mc.RegisterTransaction(txID, tx)

	id := "11111111-1111-1111-1111-111111111111"
	out, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget A"},
		TxID:   txID,
	})
	if hostErr != nil {
		t.Fatalf("create failed: %+v", hostErr)
	}
	if out.Record["name"] != "Widget A" {
		t.Errorf("Record[name] = %v, want Widget A", out.Record["name"])
	}

	if _, ok := mc.Transaction(txID); !ok {
		t.Fatal("expected the transaction to still be registered after ORMCreate")
	}
	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE id = '` + id + `'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("row visible to a separate connection before commit, want invisible (count = %d)", count)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", slug); got != 0 {
		t.Errorf("orm.record.created jobs = %d, want 0 before commit", got)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE id = '` + id + `'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("row count after commit = %d, want 1", count)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", slug); got != 1 {
		t.Errorf("orm.record.created jobs after commit = %d, want 1", got)
	}
}

// TestHostORM_Write_TxID_RollbackUndoesWrite proves ORMWrite's effects
// roll back along with the caller's own borrowed transaction — ORMWrite
// itself must never have committed anything for a partial effect to
// survive.
func TestHostORM_Write_TxID_RollbackUndoesWrite(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritetxtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	insertClient := r.EventInsertClient()
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	id := "11111111-1111-1111-1111-111111111111"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Original"},
	}); hostErr != nil {
		t.Fatalf("create failed: %+v", hostErr)
	}

	tx, err := beginTenantScopedWrite(ctx, primaryDB, mc)
	if err != nil {
		t.Fatalf("beginTenantScopedWrite: %v", err)
	}
	const txID = "test-tx-2"
	mc.RegisterTransaction(txID, tx)

	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, ORMWriteInput{
		Model:  "testmodule.item",
		ID:     id,
		Record: map[string]any{"name": "Changed"},
		TxID:   txID,
	}); hostErr != nil {
		t.Fatalf("write failed: %+v", hostErr)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var name string
	if err := primaryDB.QueryRow(`SELECT name FROM tenant_` + slug + `.item WHERE id = '` + id + `'`).Scan(&name); err != nil {
		t.Fatalf("query name: %v", err)
	}
	if name != "Original" {
		t.Errorf("name = %q after rollback, want %q — ORMWrite must not have committed anything itself", name, "Original")
	}
}

func TestHostORM_Create_TxIDNotFound(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreatetxnotfoundtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	insertClient := r.EventInsertClient()
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	_, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A"},
		TxID:   "does-not-exist",
	})
	if hostErr == nil {
		t.Fatal("expected an error for an unregistered tx_id")
	}
	if hostErr.Code != abi.ErrCodeTransactionNotFound {
		t.Errorf("Error.Code = %q, want %q", hostErr.Code, abi.ErrCodeTransactionNotFound)
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

func TestHostORM_Create_ReadonlyField_FieldNotWritable(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreatereadonlytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureLockedItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{readonlyFieldModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model: "testmodule.locked_item",
		Record: map[string]any{
			"id":           "11111111-1111-1111-1111-111111111111",
			"tenant_id":    "00000000-0000-0000-0000-000000000001",
			"name":         "Widget",
			"internal_ref": "should not be settable",
		},
	}, nil)
	if env.OK {
		t.Fatal("expected create to fail: internal_ref is a readonly field")
	}
	if env.Error.Code != abi.ErrCodeFieldNotWritable {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeFieldNotWritable)
	}
	if env.Error.Details["field"] != "internal_ref" {
		t.Errorf("Error.Details[field] = %v, want internal_ref", env.Error.Details["field"])
	}
}

func TestHostORM_Write_ReadonlyField_FieldNotWritable(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritereadonlytest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureLockedItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{readonlyFieldModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.locked_item",
		Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget"},
	}, nil); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}

	env := callORMHost(t, ctx, inst, "call_write", ORMWriteInput{
		Model:  "testmodule.locked_item",
		ID:     id,
		Record: map[string]any{"internal_ref": "should not be settable"},
	}, nil)
	if env.OK {
		t.Fatal("expected write to fail: internal_ref is a readonly field")
	}
	if env.Error.Code != abi.ErrCodeFieldNotWritable {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeFieldNotWritable)
	}
	if env.Error.Details["field"] != "internal_ref" {
		t.Errorf("Error.Details[field] = %v, want internal_ref", env.Error.Details["field"])
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

	var out ExecResult
	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{Model: "testmodule.item", IDs: []string{id}}, &out)
	if !env.OK {
		t.Fatalf("unlink failed: %+v", env.Error)
	}
	if out.Count != 1 || len(out.IDs) != 1 || out.IDs[0] != id {
		t.Errorf("ExecResult = %+v, want Count=1 IDs=[%s]", out, id)
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

	var out ExecResult
	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{Model: "testmodule.hard_item", IDs: []string{id}}, &out)
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

	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{Model: "testmodule.item", IDs: []string{"99999999-9999-9999-9999-999999999999"}}, nil)
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

	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{Model: "testmodule.hard_item", IDs: []string{parentID}}, nil)
	if env.OK {
		t.Fatal("expected a Restrict FK violation to fail")
	}
	if env.Error.Code != abi.ErrCodeForeignKeyViolation {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeForeignKeyViolation)
	}
}

func TestHostORM_Unlink_BulkDeletesAllInOneTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormunlinkbulkok%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	ids := []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"}
	for i, id := range ids {
		if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
			Model:  "testmodule.item",
			Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": fmt.Sprintf("Item %d", i)},
		}, nil); !env.OK {
			t.Fatalf("create %d failed: %+v", i, env.Error)
		}
	}

	var out ExecResult
	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{Model: "testmodule.item", IDs: ids}, &out)
	if !env.OK {
		t.Fatalf("unlink failed: %+v", env.Error)
	}
	if out.Count != 2 {
		t.Errorf("Count = %d, want 2", out.Count)
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE deleted_at IS NULL`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("remaining non-deleted row count = %d, want 0", count)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.deleted", slug); got != 2 {
		t.Errorf("orm.record.deleted jobs = %d, want 2 (one per affected record, not batched)", got)
	}
}

func TestHostORM_Unlink_MissingIDInBatch_AbortsWholeBatch(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormunlinkbulkabort%d", time.Now().UnixNano())
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

	env := callORMHost(t, ctx, inst, "call_unlink", ORMUnlinkInput{
		Model: "testmodule.item", IDs: []string{id, "99999999-9999-9999-9999-999999999999"},
	}, nil)
	if env.OK {
		t.Fatal("expected a missing ID to fail the whole batch")
	}
	if env.Error.Code != abi.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeNotFound)
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_`+slug+`.item WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1 (the first ID's delete should have rolled back too)", count)
	}
}

// --- goerp#380: create_batch, first_or_create, write_many, write_where, OnConflict* ---

func TestHostORM_CreateBatch_AllOrNothing_OneFailureAbortsWholeBatch(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreatebatchabort%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_create_batch", ORMCreateBatchInput{
		Model: "testmodule.item",
		Records: []map[string]any{
			{"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A"},
			{"id": "22222222-2222-2222-2222-222222222222", "tenant_id": "00000000-0000-0000-0000-000000000001"}, // missing required "name"
		},
	}, nil)
	if env.OK {
		t.Fatal("expected a missing-required-field record to fail the whole batch")
	}
	if env.Error.Code != abi.ErrCodeValidationFailed {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeValidationFailed)
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("row count = %d, want 0 (the first record's insert should have rolled back too)", count)
	}
}

func TestHostORM_CreateBatch_Succeeds_EmitsOneBatchedEvent(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreatebatchok%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var out ORMCreateBatchOutput
	env := callORMHost(t, ctx, inst, "call_create_batch", ORMCreateBatchInput{
		Model: "testmodule.item",
		Records: []map[string]any{
			{"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A"},
			{"id": "22222222-2222-2222-2222-222222222222", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "B"},
		},
	}, &out)
	if !env.OK {
		t.Fatalf("create_batch failed: %+v", env.Error)
	}
	if len(out.Records) != 2 {
		t.Fatalf("len(Records) = %d, want 2", len(out.Records))
	}

	// One batched event, not one per record.
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", slug); got != 1 {
		t.Errorf("orm.record.created jobs = %d, want 1 (batched)", got)
	}
}

func TestHostORM_Create_OnConflictIgnore_NoErrorNoEvent(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormconflictignore%d", time.Now().UnixNano())
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

	second := ORMCreateInput{
		Model:      "testmodule.item",
		Record:     map[string]any{"id": "22222222-2222-2222-2222-222222222222", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "B", "code": "DUP"},
		OnConflict: &OnConflictOption{Fields: []string{"code"}, Policy: "ignore"},
	}
	var out ORMCreateOutput
	env := callORMHost(t, ctx, inst, "call_create", second, &out)
	if !env.OK {
		t.Fatalf("OnConflictIgnore create should not fail: %+v", env.Error)
	}
	if out.Record != nil {
		t.Errorf("Record = %+v, want nil (nothing was created)", out.Record)
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE code = 'DUP'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1 (the conflicting insert should have been skipped, not applied)", count)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", slug); got != 1 {
		t.Errorf("orm.record.created jobs = %d, want 1 (only the first create, none for the ignored conflict)", got)
	}
}

func TestHostORM_Create_OnConflictUpdate_EmitsUpdatedNotCreated(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormconflictupdate%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	first := ORMCreateInput{Model: "testmodule.item", Record: map[string]any{
		"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A", "code": "DUP2",
	}}
	if env := callORMHost(t, ctx, inst, "call_create", first, nil); !env.OK {
		t.Fatalf("first create failed: %+v", env.Error)
	}

	second := ORMCreateInput{
		Model:      "testmodule.item",
		Record:     map[string]any{"id": "22222222-2222-2222-2222-222222222222", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "B updated", "code": "DUP2"},
		OnConflict: &OnConflictOption{Fields: []string{"code"}, Policy: "update"},
	}
	var out ORMCreateOutput
	env := callORMHost(t, ctx, inst, "call_create", second, &out)
	if !env.OK {
		t.Fatalf("OnConflictUpdate create failed: %+v", env.Error)
	}
	if out.Record["name"] != "B updated" {
		t.Errorf("Record[name] = %v, want %q", out.Record["name"], "B updated")
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE code = 'DUP2'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1 (the conflicting row was updated in place, not duplicated)", count)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", slug); got != 1 {
		t.Errorf("orm.record.created jobs = %d, want 1 (only the first, real create)", got)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", slug); got != 1 {
		t.Errorf("orm.record.updated jobs = %d, want 1 (the OnConflictUpdate row, not orm.record.created)", got)
	}
}

func TestHostORM_Create_OnConflict_InvalidTarget_ConflictTargetInvalid(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormconflicttarget%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:      "testmodule.item",
		Record:     map[string]any{"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A"},
		OnConflict: &OnConflictOption{Fields: []string{"name"}, Policy: "ignore"}, // "name" has no unique index
	}, nil)
	if env.OK {
		t.Fatal("expected a conflict target with no matching unique index to fail")
	}
	if env.Error.Code != abi.ErrCodeConflictTargetInvalid {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeConflictTargetInvalid)
	}
}

func TestHostORM_FirstOrCreate_ExistingRecord_CreatedFalse(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfochit%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "A", "code": "FOC-1"},
	}, nil); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}

	var out ORMFirstOrCreateOutput
	env := callORMHost(t, ctx, inst, "call_first_or_create", ORMFirstOrCreateInput{
		Model:  "testmodule.item",
		Domain: "record.code = 'FOC-1'",
		Record: map[string]any{"id": "99999999-9999-9999-9999-999999999999", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "should not be created", "code": "FOC-1"},
	}, &out)
	if !env.OK {
		t.Fatalf("first_or_create failed: %+v", env.Error)
	}
	if out.Created {
		t.Error("Created = true, want false (record already existed)")
	}
	if out.Record["id"] != id {
		t.Errorf("Record[id] = %v, want %v (the existing record)", out.Record["id"], id)
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE code = 'FOC-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1 (no duplicate created)", count)
	}
}

func TestHostORM_FirstOrCreate_MissingRecord_CreatedTrue(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfocmiss%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var out ORMFirstOrCreateOutput
	env := callORMHost(t, ctx, inst, "call_first_or_create", ORMFirstOrCreateInput{
		Model:  "testmodule.item",
		Domain: "record.code = 'FOC-2'",
		Record: map[string]any{"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "New", "code": "FOC-2"},
	}, &out)
	if !env.OK {
		t.Fatalf("first_or_create failed: %+v", env.Error)
	}
	if !out.Created {
		t.Error("Created = false, want true (no matching record existed)")
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", slug); got != 1 {
		t.Errorf("orm.record.created jobs = %d, want 1", got)
	}
}

// TestHostORM_FirstOrCreate_ConcurrentCallersRacingSameDomain_NeverDuplicates
// drives two goroutines through ORMFirstOrCreate directly (bypassing the
// WASM instance layer — a single ModuleInstance isn't safe for concurrent
// calls, but the advisory lock this test is actually verifying is a
// Postgres-level primitive, keyed the same way regardless of which Go
// call reaches it) racing the identical (tenant, model, domain) triple.
// Exactly one should observe Created=true.
func TestHostORM_FirstOrCreate_ConcurrentCallersRacingSameDomain_NeverDuplicates(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfocrace%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	testRuntime := newHostDBTestRuntime(t, primaryDB, 10)
	insertClient := testRuntime.EventInsertClient()
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	const n = 8
	var wg sync.WaitGroup
	results := make([]ORMFirstOrCreateOutput, n)
	errs := make([]*abi.HostError, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, hostErr := ORMFirstOrCreate(ctx, testRuntime, primaryDB, insertClient, mc, ORMFirstOrCreateInput{
				Model:  "testmodule.item",
				Domain: "record.code = 'FOC-RACE'",
				Record: map[string]any{
					"id": fmt.Sprintf("%08d-0000-0000-0000-000000000000", i), "tenant_id": "00000000-0000-0000-0000-000000000001",
					"name": "Race", "code": "FOC-RACE",
				},
			})
			results[i] = out
			errs[i] = hostErr
		}(i)
	}
	wg.Wait()

	createdCount := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("call %d failed: %+v", i, errs[i])
		}
		if results[i].Created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Errorf("createdCount = %d, want exactly 1 across %d concurrent callers racing the same domain", createdCount, n)
	}

	var rowCount int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE code = 'FOC-RACE'`).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("row count = %d, want 1 (no duplicates)", rowCount)
	}
}

func TestHostORM_WriteMany_UpdatesAllInOneTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritemanyok%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	ids := []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"}
	for i, id := range ids {
		if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
			Model:  "testmodule.item",
			Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": fmt.Sprintf("Item %d", i)},
		}, nil); !env.OK {
			t.Fatalf("create %d failed: %+v", i, env.Error)
		}
	}

	var out ExecResult
	env := callORMHost(t, ctx, inst, "call_write_many", ORMWriteManyInput{
		Model: "testmodule.item", IDs: ids, Record: map[string]any{"name": "Bulk renamed"},
	}, &out)
	if !env.OK {
		t.Fatalf("write_many failed: %+v", env.Error)
	}
	if out.Count != 2 {
		t.Errorf("Count = %d, want 2", out.Count)
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE name = 'Bulk renamed'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Errorf("renamed row count = %d, want 2", count)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", slug); got != 2 {
		t.Errorf("orm.record.updated jobs = %d, want 2 (one per affected record, not batched)", got)
	}
}

func TestHostORM_WriteMany_MissingID_AbortsWholeBatch(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritemanyabort%d", time.Now().UnixNano())
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

	env := callORMHost(t, ctx, inst, "call_write_many", ORMWriteManyInput{
		Model: "testmodule.item", IDs: []string{id, "99999999-9999-9999-9999-999999999999"}, Record: map[string]any{"name": "Renamed"},
	}, nil)
	if env.OK {
		t.Fatal("expected a missing ID to fail the whole batch")
	}
	if env.Error.Code != abi.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeNotFound)
	}

	var name string
	if err := primaryDB.QueryRow(`SELECT name FROM tenant_`+slug+`.item WHERE id = $1`, id).Scan(&name); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if name != "A" {
		t.Errorf("name = %q, want %q (the first ID's update should have rolled back too)", name, "A")
	}
}

func TestHostORM_WriteWhere_UpdatesMatchingRows(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritewhereok%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	// "code" carries a unique index, so each row (matching or not) needs
	// its own distinct value — the domain below matches by IN(...) over
	// two of the three codes rather than a shared value.
	matching := []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"}
	matchingCodes := []string{"WHERE-1", "WHERE-2"}
	for i, id := range matching {
		if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
			Model:  "testmodule.item",
			Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": fmt.Sprintf("Match %d", i), "code": matchingCodes[i]},
		}, nil); !env.OK {
			t.Fatalf("create matching %d failed: %+v", i, env.Error)
		}
	}
	nonMatchingID := "33333333-3333-3333-3333-333333333333"
	if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": nonMatchingID, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "No match", "code": "WHERE-OTHER"},
	}, nil); !env.OK {
		t.Fatalf("create non-matching failed: %+v", env.Error)
	}

	var out ExecResult
	env := callORMHost(t, ctx, inst, "call_write_where", ORMWriteWhereInput{
		Model: "testmodule.item", Domain: "record.code IN ('WHERE-1', 'WHERE-2')", Record: map[string]any{"name": "Bulk via domain"},
	}, &out)
	if !env.OK {
		t.Fatalf("write_where failed: %+v", env.Error)
	}
	if out.Count != 2 {
		t.Errorf("Count = %d, want 2", out.Count)
	}

	var nonMatchName string
	if err := primaryDB.QueryRow(`SELECT name FROM tenant_`+slug+`.item WHERE id = $1`, nonMatchingID).Scan(&nonMatchName); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if nonMatchName != "No match" {
		t.Errorf("non-matching row's name = %q, want unchanged %q", nonMatchName, "No match")
	}
}

func TestHostORM_WriteWhere_MalformedDomain_DomainInvalid(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritewherebad%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_write_where", ORMWriteWhereInput{
		Model: "testmodule.item", Domain: "record.code == ???", Record: map[string]any{"name": "X"},
	}, nil)
	if env.OK {
		t.Fatal("expected a malformed domain to fail")
	}
	if env.Error.Code != abi.ErrCodeDomainInvalid {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeDomainInvalid)
	}
}

func TestHostORM_WriteWhere_ValueWithSingleQuote_SafelyEscaped(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritewhereinj%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	if env := callORMHost(t, ctx, inst, "call_create", ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"id": id, "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "O'Brien", "code": "INJ-1"},
	}, nil); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}

	var out ExecResult
	env := callORMHost(t, ctx, inst, "call_write_where", ORMWriteWhereInput{
		Model: "testmodule.item", Domain: "record.name = 'O''Brien'", Record: map[string]any{"name": "Renamed"},
	}, &out)
	if !env.OK {
		t.Fatalf("write_where with an escaped literal failed: %+v", env.Error)
	}
	if out.Count != 1 {
		t.Errorf("Count = %d, want 1", out.Count)
	}

	var stillExists int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item`).Scan(&stillExists); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if stillExists != 1 {
		t.Errorf("row count = %d, want 1 (the table itself, not dropped or otherwise disturbed)", stillExists)
	}
}
