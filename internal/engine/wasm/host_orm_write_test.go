package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
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

// newORMWriteTestModuleContext returns a ModuleContext plus the real UUID
// it set as TenantID. TenantSlug stays tenantSlug (the per-test schema
// name), but TenantID is now a freshly generated UUID, not the slug
// itself: goerp#992 made tenant_id Readonly, so a create that omits it
// now gets it auto-filled straight from ModuleContext.TenantID
// (fillCreateServerFields) into the item table's real UUID tenant_id
// column — a non-UUID slug there would fail to insert. The returned UUID
// is also what a caller should pass to countEventDeliveryJobsByName for
// river_job isolation, in place of the slug that filled that role before.
func newORMWriteTestModuleContext(tenantSlug string, modelDecls []model.ModelDeclaration) (*ModuleContext, string) {
	tenantID := uuid.New().String()
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, tenantID, tenantSlug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: modelDecls}), tenantID
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
			{Name: "id", Def: model.UUID().Required().PrimaryKey().Default("uuidv7()")},
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
		id UUID PRIMARY KEY DEFAULT uuidv7(),
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
		id UUID PRIMARY KEY DEFAULT uuidv7(),
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
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var out abiv1.ORMCreateOutput
	env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "Widget A"},
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

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", tenantID); got != 1 {
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
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	const txID = "test-tx-1"
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, txID)
	defer func() { _ = tx.Rollback() }()

	out, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "Widget A"},
		TxID:   txID,
	})
	if hostErr != nil {
		t.Fatalf("create failed: %+v", hostErr)
	}
	if out.Record["name"] != "Widget A" {
		t.Errorf("Record[name] = %v, want Widget A", out.Record["name"])
	}
	id, _ := out.Record["id"].(string)

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
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", tenantID); got != 0 {
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
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", tenantID); got != 1 {
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	createOut, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "Original"},
	})
	if hostErr != nil {
		t.Fatalf("create failed: %+v", hostErr)
	}
	id, _ := createOut.Record["id"].(string)

	const txID = "test-tx-2"
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, txID)

	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMWriteInput{
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	_, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "A"},
		TxID:   "does-not-exist",
	})
	if hostErr == nil {
		t.Fatal("expected an error for an unregistered tx_id")
	}
	if hostErr.Code != abiv1.ErrCodeTransactionNotFound {
		t.Errorf("Error.Code = %q, want %q", hostErr.Code, abiv1.ErrCodeTransactionNotFound)
	}
}

func TestHostORM_Create_MissingRequiredField_ValidationFailed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormcreaterequiredtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{},
	}, nil)
	if env.OK {
		t.Fatal("expected create to fail on a missing required field")
	}
	if env.Error.Code != abiv1.ErrCodeValidationFailed {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeValidationFailed)
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{readonlyFieldModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	// Only internal_ref goes in the record — goerp#992 made id/tenant_id
	// Readonly too, and buildAssignment iterates a map (unordered), so
	// including either alongside internal_ref would make which field
	// name lands in Error.Details["field"] nondeterministic.
	env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model: "testmodule.locked_item",
		Record: map[string]any{
			"name":         "Widget",
			"internal_ref": "should not be settable",
		},
	}, nil)
	if env.OK {
		t.Fatal("expected create to fail: internal_ref is a readonly field")
	}
	if env.Error.Code != abiv1.ErrCodeFieldNotWritable {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeFieldNotWritable)
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{readonlyFieldModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var createOut abiv1.ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.locked_item",
		Record: map[string]any{"name": "Widget"},
	}, &createOut); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id, _ := createOut.Record["id"].(string)

	env := callORMHost(t, ctx, inst, "call_write", abiv1.ORMWriteInput{
		Model:  "testmodule.locked_item",
		ID:     id,
		Record: map[string]any{"internal_ref": "should not be settable"},
	}, nil)
	if env.OK {
		t.Fatal("expected write to fail: internal_ref is a readonly field")
	}
	if env.Error.Code != abiv1.ErrCodeFieldNotWritable {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeFieldNotWritable)
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	first := abiv1.ORMCreateInput{Model: "testmodule.item", Record: map[string]any{
		"name": "A", "code": "DUP",
	}}
	if env := callORMHost(t, ctx, inst, "call_create", first, nil); !env.OK {
		t.Fatalf("first create failed: %+v", env.Error)
	}

	second := abiv1.ORMCreateInput{Model: "testmodule.item", Record: map[string]any{
		"name": "B", "code": "DUP",
	}}
	env := callORMHost(t, ctx, inst, "call_create", second, nil)
	if env.OK {
		t.Fatal("expected a unique violation on duplicate code")
	}
	if env.Error.Code != abiv1.ErrCodeUniqueViolation {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeUniqueViolation)
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
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var created abiv1.ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "A"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id, _ := created.Record["id"].(string)
	originalEtag := created.Record["etag"].(string)

	var out abiv1.ORMWriteOutput
	env := callORMHost(t, ctx, inst, "call_write", abiv1.ORMWriteInput{
		Model: "testmodule.item", ID: id, Record: map[string]any{"name": "A renamed"}, ExpectedEtag: new(originalEtag),
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

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", tenantID); got != 1 {
		t.Errorf("orm.record.updated jobs = %d, want 1", got)
	}
}

// TestHostORM_Write_EtagFromCreate_EnforcesCAS: a created record has a
// real etag, a write expecting it succeeds once, and a second write reusing
// it fails. An expected etag of "" is a precondition too, not "no
// precondition" (goerp#871), so it fails against the record's real etag.
func TestHostORM_Write_EtagFromCreate_EnforcesCAS(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritecreateetagtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var created abiv1.ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "A"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id, _ := created.Record["id"].(string)
	createdEtag, _ := created.Record["etag"].(string)
	if createdEtag == "" {
		t.Fatal("Record[etag] after create is empty, want an engine-generated etag")
	}

	env := callORMHost(t, ctx, inst, "call_write", abiv1.ORMWriteInput{
		Model: "testmodule.item", ID: id, Record: map[string]any{"name": "Blank etag"}, ExpectedEtag: new(""),
	}, nil)
	if env.OK || env.Error.Code != abiv1.ErrCodeEtagMismatch {
		t.Fatalf("write expecting \"\" = %+v, want %s", env.Error, abiv1.ErrCodeEtagMismatch)
	}

	var firstOut abiv1.ORMWriteOutput
	if env := callORMHost(t, ctx, inst, "call_write", abiv1.ORMWriteInput{
		Model: "testmodule.item", ID: id, Record: map[string]any{"name": "First writer"}, ExpectedEtag: new(createdEtag),
	}, &firstOut); !env.OK {
		t.Fatalf("first write (against the created etag) failed: %+v", env.Error)
	}

	env = callORMHost(t, ctx, inst, "call_write", abiv1.ORMWriteInput{
		Model: "testmodule.item", ID: id, Record: map[string]any{"name": "Second writer"}, ExpectedEtag: new(createdEtag),
	}, nil)
	if env.OK {
		t.Fatal("expected a second write reusing the stale created etag to fail")
	}
	if env.Error.Code != abiv1.ErrCodeEtagMismatch {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeEtagMismatch)
	}
}

func TestHostORM_Write_StaleEtag_EtagMismatch(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritestaletest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var created abiv1.ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "A"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id, _ := created.Record["id"].(string)

	env := callORMHost(t, ctx, inst, "call_write", abiv1.ORMWriteInput{
		Model: "testmodule.item", ID: id, Record: map[string]any{"name": "A renamed"}, ExpectedEtag: new("stale-etag"),
	}, nil)
	if env.OK {
		t.Fatal("expected a stale etag to fail")
	}
	if env.Error.Code != abiv1.ErrCodeEtagMismatch {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeEtagMismatch)
	}
}

func TestHostORM_Write_MissingRecord_NotFound(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritemissingtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_write", abiv1.ORMWriteInput{
		Model: "testmodule.item", ID: "99999999-9999-9999-9999-999999999999", Record: map[string]any{"name": "X"}, ExpectedEtag: new("whatever"),
	}, nil)
	if env.OK {
		t.Fatal("expected a missing record to fail")
	}
	if env.Error.Code != abiv1.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeNotFound)
	}
}

func TestHostORM_Unlink_SoftDeletesWithStandardFields(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormunlinksofttest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var created abiv1.ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "A"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id, _ := created.Record["id"].(string)

	var out abiv1.ORMExecResult
	env := callORMHost(t, ctx, inst, "call_unlink", abiv1.ORMUnlinkInput{Model: "testmodule.item", IDs: []string{id}}, &out)
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

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.deleted", tenantID); got != 1 {
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	id := "11111111-1111-1111-1111-111111111111"
	if _, err := primaryDB.Exec(`INSERT INTO tenant_`+slug+`.hard_item (id, name) VALUES ($1, 'A')`, id); err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}

	var out abiv1.ORMExecResult
	env := callORMHost(t, ctx, inst, "call_unlink", abiv1.ORMUnlinkInput{Model: "testmodule.hard_item", IDs: []string{id}}, &out)
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_unlink", abiv1.ORMUnlinkInput{Model: "testmodule.item", IDs: []string{"99999999-9999-9999-9999-999999999999"}}, nil)
	if env.OK {
		t.Fatal("expected unlink of a missing record to fail")
	}
	if env.Error.Code != abiv1.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeNotFound)
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_unlink", abiv1.ORMUnlinkInput{Model: "testmodule.hard_item", IDs: []string{parentID}}, nil)
	if env.OK {
		t.Fatal("expected a Restrict FK violation to fail")
	}
	if env.Error.Code != abiv1.ErrCodeForeignKeyViolation {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeForeignKeyViolation)
	}
}

func TestHostORM_Unlink_BulkDeletesAllInOneTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormunlinkbulkok%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	ids := make([]string, 2)
	for i := range ids {
		var created abiv1.ORMCreateOutput
		if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
			Model:  "testmodule.item",
			Record: map[string]any{"name": fmt.Sprintf("Item %d", i)},
		}, &created); !env.OK {
			t.Fatalf("create %d failed: %+v", i, env.Error)
		}
		ids[i], _ = created.Record["id"].(string)
	}

	var out abiv1.ORMExecResult
	env := callORMHost(t, ctx, inst, "call_unlink", abiv1.ORMUnlinkInput{Model: "testmodule.item", IDs: ids}, &out)
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
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.deleted", tenantID); got != 2 {
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var created abiv1.ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "A"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id, _ := created.Record["id"].(string)

	env := callORMHost(t, ctx, inst, "call_unlink", abiv1.ORMUnlinkInput{
		Model: "testmodule.item", IDs: []string{id, "99999999-9999-9999-9999-999999999999"},
	}, nil)
	if env.OK {
		t.Fatal("expected a missing ID to fail the whole batch")
	}
	if env.Error.Code != abiv1.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeNotFound)
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_create_batch", abiv1.ORMCreateBatchInput{
		Model: "testmodule.item",
		Records: []map[string]any{
			{"name": "A"},
			{}, // missing required "name"
		},
	}, nil)
	if env.OK {
		t.Fatal("expected a missing-required-field record to fail the whole batch")
	}
	if env.Error.Code != abiv1.ErrCodeValidationFailed {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeValidationFailed)
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
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var out abiv1.ORMCreateBatchOutput
	env := callORMHost(t, ctx, inst, "call_create_batch", abiv1.ORMCreateBatchInput{
		Model: "testmodule.item",
		Records: []map[string]any{
			{"name": "A"},
			{"name": "B"},
		},
	}, &out)
	if !env.OK {
		t.Fatalf("create_batch failed: %+v", env.Error)
	}
	if len(out.Records) != 2 {
		t.Fatalf("len(Records) = %d, want 2", len(out.Records))
	}

	// One batched event, not one per record.
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", tenantID); got != 1 {
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
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	first := abiv1.ORMCreateInput{Model: "testmodule.item", Record: map[string]any{
		"name": "A", "code": "DUP",
	}}
	if env := callORMHost(t, ctx, inst, "call_create", first, nil); !env.OK {
		t.Fatalf("first create failed: %+v", env.Error)
	}

	second := abiv1.ORMCreateInput{
		Model:      "testmodule.item",
		Record:     map[string]any{"name": "B", "code": "DUP"},
		OnConflict: &abiv1.ORMOnConflict{Fields: []string{"code"}, Policy: "ignore"},
	}
	var out abiv1.ORMCreateOutput
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
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", tenantID); got != 1 {
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
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	first := abiv1.ORMCreateInput{Model: "testmodule.item", Record: map[string]any{
		"name": "A", "code": "DUP2",
	}}
	if env := callORMHost(t, ctx, inst, "call_create", first, nil); !env.OK {
		t.Fatalf("first create failed: %+v", env.Error)
	}

	second := abiv1.ORMCreateInput{
		Model:      "testmodule.item",
		Record:     map[string]any{"name": "B updated", "code": "DUP2"},
		OnConflict: &abiv1.ORMOnConflict{Fields: []string{"code"}, Policy: "update"},
	}
	var out abiv1.ORMCreateOutput
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
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", tenantID); got != 1 {
		t.Errorf("orm.record.created jobs = %d, want 1 (only the first, real create)", got)
	}
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", tenantID); got != 1 {
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:      "testmodule.item",
		Record:     map[string]any{"name": "A"},
		OnConflict: &abiv1.ORMOnConflict{Fields: []string{"name"}, Policy: "ignore"}, // "name" has no unique index
	}, nil)
	if env.OK {
		t.Fatal("expected a conflict target with no matching unique index to fail")
	}
	if env.Error.Code != abiv1.ErrCodeConflictTargetInvalid {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeConflictTargetInvalid)
	}
}

func TestHostORM_FirstOrCreate_ExistingRecord_CreatedFalse(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfochit%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var created abiv1.ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "A", "code": "FOC-1"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id, _ := created.Record["id"].(string)

	var out abiv1.ORMFirstOrCreateOutput
	env := callORMHost(t, ctx, inst, "call_first_or_create", abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.item",
		UniqueVals: map[string]any{"code": "FOC-1"},
		CreateVals: map[string]any{"name": "should not be created"},
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

func TestHostORM_FirstOrCreate_InvalidTarget_ConflictTargetInvalid(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfocinvalidtarget%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_first_or_create", abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.item",
		UniqueVals: map[string]any{"name": "A"}, // "name" has no unique index
		CreateVals: map[string]any{"name": "A"},
	}, nil)
	if env.OK {
		t.Fatal("expected a unique-vals field set with no matching unique index to fail")
	}
	if env.Error.Code != abiv1.ErrCodeConflictTargetInvalid {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeConflictTargetInvalid)
	}
}

func TestHostORM_FirstOrCreate_MissingRecord_CreatedTrue(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfocmiss%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var out abiv1.ORMFirstOrCreateOutput
	env := callORMHost(t, ctx, inst, "call_first_or_create", abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.item",
		UniqueVals: map[string]any{"code": "FOC-2"},
		// "code" is absent from CreateVals — the miss-path merge should supply it.
		CreateVals: map[string]any{"name": "New"},
	}, &out)
	if !env.OK {
		t.Fatalf("first_or_create failed: %+v", env.Error)
	}
	if !out.Created {
		t.Error("Created = false, want true (no matching record existed)")
	}
	if out.Record["code"] != "FOC-2" {
		t.Errorf("Record[code] = %v, want %q (merged in from UniqueVals)", out.Record["code"], "FOC-2")
	}

	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.created", tenantID); got != 1 {
		t.Errorf("orm.record.created jobs = %d, want 1", got)
	}
}

// TestHostORM_FirstOrCreate_ConflictingOverlapKey_UniqueValsWins verifies
// that when the same field appears in both maps with different values,
// the inserted row still satisfies UniqueVals — otherwise a second,
// identical call's match query would miss the row it just created,
// breaking the idempotency guarantee FirstOrCreate exists for.
func TestHostORM_FirstOrCreate_ConflictingOverlapKey_UniqueValsWins(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfocoverlap%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	req := abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.item",
		UniqueVals: map[string]any{"code": "FOC-3"},
		CreateVals: map[string]any{"name": "New", "code": "wrong"},
	}

	var out1 abiv1.ORMFirstOrCreateOutput
	if env := callORMHost(t, ctx, inst, "call_first_or_create", req, &out1); !env.OK {
		t.Fatalf("first call failed: %+v", env.Error)
	}
	if out1.Record["code"] != "FOC-3" {
		t.Fatalf("Record[code] = %v, want %q (UniqueVals must win over CreateVals)", out1.Record["code"], "FOC-3")
	}

	var out2 abiv1.ORMFirstOrCreateOutput
	if env := callORMHost(t, ctx, inst, "call_first_or_create", req, &out2); !env.OK {
		t.Fatalf("second call failed: %+v", env.Error)
	}
	if out2.Created {
		t.Error("Created = true on second call, want false — the first call's row should have matched")
	}
}

func TestHostORM_FirstOrCreate_NilUniqueVal_ValidationFailed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfocnil%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_first_or_create", abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.item",
		UniqueVals: map[string]any{"code": nil},
		CreateVals: map[string]any{"name": "New"},
	}, nil)
	if env.OK {
		t.Fatal("expected a nil unique_vals entry to fail rather than silently never matching")
	}
	if env.Error.Code != abiv1.ErrCodeValidationFailed {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeValidationFailed)
	}
}

// TestHostORM_FirstOrCreate_TxID_ParticipatesInCallersTransaction is
// TestHostORM_Create_TxID_ParticipatesInCallersTransaction's counterpart
// for the miss-then-insert path: ORMFirstOrCreate must never commit or
// roll back a borrowed transaction itself.
func TestHostORM_FirstOrCreate_TxID_ParticipatesInCallersTransaction(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfoctxtest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	insertClient := r.EventInsertClient()
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	const txID = "test-tx-foc-1"
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, txID)
	defer func() { _ = tx.Rollback() }()

	out, hostErr := ORMFirstOrCreate(ctx, r, primaryDB, insertClient, mc, abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.item",
		UniqueVals: map[string]any{"code": "FOC-TX-1"},
		CreateVals: map[string]any{"name": "New"},
		TxID:       txID,
	})
	if hostErr != nil {
		t.Fatalf("first_or_create failed: %+v", hostErr)
	}
	if !out.Created {
		t.Error("Created = false, want true")
	}

	var count int
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE code = 'FOC-TX-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("row visible to a separate connection before commit, want invisible (count = %d)", count)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := primaryDB.QueryRow(`SELECT count(*) FROM tenant_` + slug + `.item WHERE code = 'FOC-TX-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("row count after commit = %d, want 1", count)
	}
}

// TestHostORM_FirstOrCreate_ConcurrentCallersRacingSameDomain_NeverDuplicates
// drives two goroutines through ORMFirstOrCreate directly (bypassing the
// WASM instance layer — a single ModuleInstance isn't safe for concurrent
// calls, but the advisory lock this test is actually verifying is a
// Postgres-level primitive, keyed the same way regardless of which Go
// call reaches it) racing the identical (tenant, model, unique-vals)
// triple. Exactly one should observe Created=true.
func TestHostORM_FirstOrCreate_ConcurrentCallersRacingSameDomain_NeverDuplicates(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormfocrace%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	testRuntime := newHostDBTestRuntime(t, primaryDB, 10)
	insertClient := testRuntime.EventInsertClient()
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})

	const n = 8
	var wg sync.WaitGroup
	results := make([]abiv1.ORMFirstOrCreateOutput, n)
	errs := make([]*abiv1.HostError, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, hostErr := ORMFirstOrCreate(ctx, testRuntime, primaryDB, insertClient, mc, abiv1.ORMFirstOrCreateInput{
				Model:      "testmodule.item",
				UniqueVals: map[string]any{"code": "FOC-RACE"},
				CreateVals: map[string]any{
					"name": "Race",
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
	mc, tenantID := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	ids := make([]string, 2)
	for i := range ids {
		var created abiv1.ORMCreateOutput
		if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
			Model:  "testmodule.item",
			Record: map[string]any{"name": fmt.Sprintf("Item %d", i)},
		}, &created); !env.OK {
			t.Fatalf("create %d failed: %+v", i, env.Error)
		}
		ids[i], _ = created.Record["id"].(string)
	}

	var out abiv1.ORMExecResult
	env := callORMHost(t, ctx, inst, "call_write_many", abiv1.ORMWriteManyInput{
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
	if got := countEventDeliveryJobsByName(t, primaryDB, "orm.record.updated", tenantID); got != 2 {
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	var created abiv1.ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "A"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id, _ := created.Record["id"].(string)

	env := callORMHost(t, ctx, inst, "call_write_many", abiv1.ORMWriteManyInput{
		Model: "testmodule.item", IDs: []string{id, "99999999-9999-9999-9999-999999999999"}, Record: map[string]any{"name": "Renamed"},
	}, nil)
	if env.OK {
		t.Fatal("expected a missing ID to fail the whole batch")
	}
	if env.Error.Code != abiv1.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeNotFound)
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	// "code" carries a unique index, so each row (matching or not) needs
	// its own distinct value — the domain below matches by IN(...) over
	// two of the three codes rather than a shared value.
	matchingCodes := []string{"WHERE-1", "WHERE-2"}
	for i, code := range matchingCodes {
		if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
			Model:  "testmodule.item",
			Record: map[string]any{"name": fmt.Sprintf("Match %d", i), "code": code},
		}, nil); !env.OK {
			t.Fatalf("create matching %d failed: %+v", i, env.Error)
		}
	}
	var nonMatching abiv1.ORMCreateOutput
	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "No match", "code": "WHERE-OTHER"},
	}, &nonMatching); !env.OK {
		t.Fatalf("create non-matching failed: %+v", env.Error)
	}
	nonMatchingID, _ := nonMatching.Record["id"].(string)

	var out abiv1.ORMExecResult
	env := callORMHost(t, ctx, inst, "call_write_where", abiv1.ORMWriteWhereInput{
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
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	env := callORMHost(t, ctx, inst, "call_write_where", abiv1.ORMWriteWhereInput{
		Model: "testmodule.item", Domain: "record.code == ???", Record: map[string]any{"name": "X"},
	}, nil)
	if env.OK {
		t.Fatal("expected a malformed domain to fail")
	}
	if env.Error.Code != abiv1.ErrCodeDomainInvalid {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abiv1.ErrCodeDomainInvalid)
	}
}

func TestHostORM_WriteWhere_ValueWithSingleQuote_SafelyEscaped(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("ormwritewhereinj%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{itemModelDecl()})
	inst := newHostORMWriteCaller(t, ctx, r, mc)

	if env := callORMHost(t, ctx, inst, "call_create", abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "O'Brien", "code": "INJ-1"},
	}, nil); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}

	var out abiv1.ORMExecResult
	env := callORMHost(t, ctx, inst, "call_write_where", abiv1.ORMWriteWhereInput{
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

func newServerFieldsFixture(t *testing.T, userID string) (*Runtime, *ModuleContext, string, string) {
	t.Helper()
	primaryDB := openTestPrimaryDB(t)
	slug := fmt.Sprintf("ormservfields%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureItemsTable(t, primaryDB, slug)

	tenantID := "22222222-2222-2222-2222-222222222222"
	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := NewModuleContext("req-1", "testmodule", userID, "contact-1", []string{"admin"}, nil, tenantID, slug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: []model.ModelDeclaration{itemModelDecl()}})
	return r, mc, tenantID, slug
}

func TestORMCreate_FillsTenantAndCreatedByFromTheRequest(t *testing.T) {
	const userID = "33333333-3333-3333-3333-333333333333"
	r, mc, tenantID, _ := newServerFieldsFixture(t, userID)

	out, hostErr := ORMCreate(context.Background(), r, openTestPrimaryDB(t), r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "Widget A"},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %v", hostErr)
	}
	if out.Record["tenant_id"] != tenantID || out.Record["created_by"] != userID {
		t.Errorf("tenant_id/created_by = %v/%v, want %s/%s", out.Record["tenant_id"], out.Record["created_by"], tenantID, userID)
	}
}

// TestORMCreate_RejectsSuppliedTenantAndCreatedBy pins goerp#992's
// Readonly decision: tenant_id/created_by are always engine-filled from
// the request's own context (fillCreateServerFields) when omitted, and a
// client that supplies either directly now gets orm.field_not_writable —
// the same rejection any other Readonly field gets — not the old
// "supplied value is kept" behavior.
func TestORMCreate_RejectsSuppliedTenantAndCreatedBy(t *testing.T) {
	r, mc, _, _ := newServerFieldsFixture(t, "33333333-3333-3333-3333-333333333333")

	const tenant, creator = "44444444-4444-4444-4444-444444444444", "55555555-5555-5555-5555-555555555555"
	_, hostErr := ORMCreate(context.Background(), r, openTestPrimaryDB(t), r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "Widget A", "tenant_id": tenant, "created_by": creator},
	})
	if hostErr == nil {
		t.Fatal("ORMCreate: want orm.field_not_writable for a client-supplied tenant_id/created_by, got success")
	}
	if hostErr.Code != abiv1.ErrCodeFieldNotWritable {
		t.Errorf("hostErr.Code = %q, want %q", hostErr.Code, abiv1.ErrCodeFieldNotWritable)
	}
}

func TestORMCreate_LeavesCreatedByNullForANonUUIDPrincipal(t *testing.T) {
	r, mc, tenantID, _ := newServerFieldsFixture(t, "system")

	out, hostErr := ORMCreate(context.Background(), r, openTestPrimaryDB(t), r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model:  "testmodule.item",
		Record: map[string]any{"name": "Widget A"},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreate: %v", hostErr)
	}
	if out.Record["tenant_id"] != tenantID || out.Record["created_by"] != nil {
		t.Errorf("tenant_id/created_by = %v/%v, want %s/nil", out.Record["tenant_id"], out.Record["created_by"], tenantID)
	}
}

func TestORMCreateBatch_FillsTenantOnEveryRecord(t *testing.T) {
	r, mc, tenantID, _ := newServerFieldsFixture(t, "33333333-3333-3333-3333-333333333333")

	out, hostErr := ORMCreateBatch(context.Background(), r, openTestPrimaryDB(t), r.EventInsertClient(), mc, abiv1.ORMCreateBatchInput{
		Model:   "testmodule.item",
		Records: []map[string]any{{"name": "A"}, {"name": "B"}},
	})
	if hostErr != nil {
		t.Fatalf("ORMCreateBatch: %v", hostErr)
	}
	if len(out.Records) != 2 {
		t.Fatalf("len(Records) = %d, want 2", len(out.Records))
	}
	for i, rec := range out.Records {
		if rec["tenant_id"] != tenantID {
			t.Errorf("record %d tenant_id = %v, want %s", i, rec["tenant_id"], tenantID)
		}
	}
}

func TestORMCreate_UpsertKeepsTheOriginalCreatedBy(t *testing.T) {
	const creator, updater = "33333333-3333-3333-3333-333333333333", "66666666-6666-6666-6666-666666666666"
	r, mc, tenantID, slug := newServerFieldsFixture(t, creator)
	db := openTestPrimaryDB(t)
	onConflict := &abiv1.ORMOnConflict{Policy: "update", Fields: []string{"code"}}

	_, hostErr := ORMCreate(context.Background(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
		Model:      "testmodule.item",
		Record:     map[string]any{"name": "Widget A", "code": "W-1"},
		OnConflict: onConflict,
	})
	if hostErr != nil {
		t.Fatalf("first ORMCreate: %v", hostErr)
	}

	other := NewModuleContext("req-2", "testmodule", updater, "contact-1", []string{"admin"}, nil, tenantID, slug, "trace-2",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: []model.ModelDeclaration{itemModelDecl()}})
	second, hostErr := ORMCreate(context.Background(), r, db, r.EventInsertClient(), nil, other, abiv1.ORMCreateInput{
		Model:      "testmodule.item",
		Record:     map[string]any{"name": "Widget A renamed", "code": "W-1"},
		OnConflict: onConflict,
	})
	if hostErr != nil {
		t.Fatalf("upsert ORMCreate: %v", hostErr)
	}
	if second.Record["name"] != "Widget A renamed" {
		t.Errorf("name = %v, want the upserted value", second.Record["name"])
	}
	if second.Record["created_by"] != creator {
		t.Errorf("created_by = %v, want the original creator %s", second.Record["created_by"], creator)
	}
}

func TestORMFirstOrCreate_FillsTenantOnTheCreatedRecord(t *testing.T) {
	r, mc, tenantID, _ := newServerFieldsFixture(t, "33333333-3333-3333-3333-333333333333")

	out, hostErr := ORMFirstOrCreate(context.Background(), r, openTestPrimaryDB(t), r.EventInsertClient(), mc, abiv1.ORMFirstOrCreateInput{
		Model:      "testmodule.item",
		UniqueVals: map[string]any{"code": "W-9"},
		CreateVals: map[string]any{"name": "Widget Z"},
	})
	if hostErr != nil {
		t.Fatalf("ORMFirstOrCreate: %v", hostErr)
	}
	if !out.Created || out.Record["tenant_id"] != tenantID {
		t.Errorf("Created/tenant_id = %v/%v, want true/%s", out.Created, out.Record["tenant_id"], tenantID)
	}
}
