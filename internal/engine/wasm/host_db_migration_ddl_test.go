package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/vmihailenco/msgpack/v5"
)

// migrationDDLWidgetModelDecl is a table the test module owns outright —
// the ordinary DropColumn case (model.MigrationContext.DropColumn against
// a table whose model the caller still declares).
func migrationDDLWidgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "widget",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "legacy_name", Def: model.Text()},
		},
	}
}

// migrationDDLUnownedModelDecl is declared in ModelDecls but deliberately
// left out of both OwnedModels and ExtendsModels — the defensive branch
// migrationDDLTableOwned rejects even though a matching declaration
// exists, since manifest-spec.md's own load-time validation should never
// let this happen for a real module (owned_models must exactly match
// get_model_declarations()) but the host function checks it anyway.
func migrationDDLUnownedModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "gadget",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
		},
	}
}

// migrationDDLExtendedModelDecl is owned by another module but extended by
// the test module — the field_extension case, granted via ExtendsModels
// rather than OwnedModels.
func migrationDDLExtendedModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "shared",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "extra", Def: model.Text()},
		},
	}
}

// OwnedModels/ExtendsModels always carry the manifest's own dotted
// "{module}.{resource}" form (manifest.SchemaConfig.OwnedModels,
// confirmed by internal/engine/manifest/manifest_test.go and
// internal/engine/schema/session_test.go) — never the bare
// model.ModelDeclaration.Name form host.orm's own resolveModel
// (host_orm.go) strips the module prefix down to before comparing. These
// fixtures use "testmodule.widget"/"testmodule.shared" to match that real
// shape, not the bare names a first, buggy version of this test suite
// used (which happened to "pass" only because the same bare-vs-dotted
// mismatch existed on both sides of the comparison).
func newMigrationDDLTestModuleContext(tenantSlug string, isDataMigrationJob bool) *ModuleContext {
	mc := NewModuleContext("req-1", "testmodule", "", "", nil, nil, tenantSlug, tenantSlug, "trace-1",
		abi.CapDBMigrationDDL, nil, ModuleSnapshot{
			ModelDecls: []model.ModelDeclaration{
				migrationDDLWidgetModelDecl(),
				migrationDDLUnownedModelDecl(),
				migrationDDLExtendedModelDecl(),
			},
			OwnedModels:   []string{"testmodule.widget"},
			ExtendsModels: []string{"testmodule.shared"},
		})
	mc.IsDataMigrationJob = isDataMigrationJob
	return mc
}

func createFixtureMigrationDDLTables(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	for _, stmt := range []string{
		`CREATE TABLE ` + schemaName + `.widget (
			id UUID PRIMARY KEY,
			tenant_id UUID NOT NULL,
			legacy_name TEXT
		)`,
		`CREATE TABLE ` + schemaName + `.gadget (id UUID PRIMARY KEY)`,
		`CREATE TABLE ` + schemaName + `.shared (
			id UUID PRIMARY KEY,
			extra TEXT
		)`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("create fixture table: %v", err)
		}
	}
}

func setupMigrationDDLTest(t *testing.T) (*sql.DB, string, *ModuleContext) {
	t.Helper()
	primaryDB := openTestPrimaryDB(t)
	slug := fmt.Sprintf("dbmigddl%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureMigrationDDLTables(t, primaryDB, slug)
	return primaryDB, slug, newMigrationDDLTestModuleContext(slug, true)
}

func columnExists(t *testing.T, conn *sql.DB, slug, table, column string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRowContext(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
		)`, "tenant_"+slug, table, column).Scan(&exists)
	if err != nil {
		t.Fatalf("check column existence: %v", err)
	}
	return exists
}

func tableExists(t *testing.T, conn *sql.DB, slug, table string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRowContext(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = $1 AND table_name = $2
		)`, "tenant_"+slug, table).Scan(&exists)
	if err != nil {
		t.Fatalf("check table existence: %v", err)
	}
	return exists
}

func TestDBMigrationDDL_DropColumn_Owned(t *testing.T) {
	primaryDB, slug, mc := setupMigrationDDLTest(t)
	ctx := context.Background()

	_, hostErr := DBMigrationDDL(ctx, primaryDB, mc, dbMigrationDDLInput{
		Op: migrationDDLOpDropColumn, Table: "widget", Column: "legacy_name",
	})
	if hostErr != nil {
		t.Fatalf("DBMigrationDDL: %+v", hostErr)
	}
	if columnExists(t, primaryDB, slug, "widget", "legacy_name") {
		t.Error("legacy_name column still exists after DropColumn")
	}
}

func TestDBMigrationDDL_DropTable_Owned(t *testing.T) {
	primaryDB, slug, mc := setupMigrationDDLTest(t)
	ctx := context.Background()

	_, hostErr := DBMigrationDDL(ctx, primaryDB, mc, dbMigrationDDLInput{
		Op: migrationDDLOpDropTable, Table: "widget",
	})
	if hostErr != nil {
		t.Fatalf("DBMigrationDDL: %+v", hostErr)
	}
	if tableExists(t, primaryDB, slug, "widget") {
		t.Error("widget table still exists after DropTable")
	}
}

func TestDBMigrationDDL_DropColumn_ViaExtendsModels(t *testing.T) {
	primaryDB, slug, mc := setupMigrationDDLTest(t)
	ctx := context.Background()

	_, hostErr := DBMigrationDDL(ctx, primaryDB, mc, dbMigrationDDLInput{
		Op: migrationDDLOpDropColumn, Table: "shared", Column: "extra",
	})
	if hostErr != nil {
		t.Fatalf("DBMigrationDDL: %+v", hostErr)
	}
	if columnExists(t, primaryDB, slug, "shared", "extra") {
		t.Error("extra column still exists after DropColumn via ExtendsModels")
	}
}

func TestDBMigrationDDL_RejectsUnownedTable(t *testing.T) {
	primaryDB, slug, mc := setupMigrationDDLTest(t)
	ctx := context.Background()

	_, hostErr := DBMigrationDDL(ctx, primaryDB, mc, dbMigrationDDLInput{
		Op: migrationDDLOpDropTable, Table: "gadget",
	})
	if hostErr == nil {
		t.Fatal("expected an error dropping a table declared but not owned/extended")
	}
	if hostErr.Code != abi.ErrCodeMigrationDDLNotOwned {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeMigrationDDLNotOwned)
	}
	if !tableExists(t, primaryDB, slug, "gadget") {
		t.Error("gadget table was dropped despite failing the ownership check")
	}
}

func TestDBMigrationDDL_RejectsUndeclaredTable(t *testing.T) {
	primaryDB, _, mc := setupMigrationDDLTest(t)
	ctx := context.Background()

	_, hostErr := DBMigrationDDL(ctx, primaryDB, mc, dbMigrationDDLInput{
		Op: migrationDDLOpDropTable, Table: "orphan_table",
	})
	if hostErr == nil {
		t.Fatal("expected an error dropping a table with no matching declaration at all")
	}
	if hostErr.Code != abi.ErrCodeMigrationDDLNotOwned {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeMigrationDDLNotOwned)
	}
}

func TestDBMigrationDDL_RejectsUnknownColumn(t *testing.T) {
	primaryDB, slug, mc := setupMigrationDDLTest(t)
	ctx := context.Background()

	_, hostErr := DBMigrationDDL(ctx, primaryDB, mc, dbMigrationDDLInput{
		Op: migrationDDLOpDropColumn, Table: "widget", Column: "does_not_exist",
	})
	if hostErr == nil {
		t.Fatal("expected an error dropping a column that doesn't exist on the real table")
	}
	if hostErr.Code != abi.ErrCodeMigrationDDLTargetNotFound {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeMigrationDDLTargetNotFound)
	}
	if !columnExists(t, primaryDB, slug, "widget", "legacy_name") {
		t.Error("unrelated legacy_name column was affected")
	}
}

// TestDBMigrationDDL_DropColumn_AlreadyRemovedFromDeclaration is
// migration-guide.md §3.11's own documented workflow ("Remove from the
// model declaration and add a handler"): the field is gone from
// ModelDecls by the time the handler runs (removed in the same change
// that adds the DropColumn call), but the table's own model is still
// owned. This must succeed — requiring the column to still be declared
// would reject the workflow the docs themselves teach.
func TestDBMigrationDDL_DropColumn_AlreadyRemovedFromDeclaration(t *testing.T) {
	primaryDB, slug, _ := setupMigrationDDLTest(t)
	ctx := context.Background()

	// A widget declaration with legacy_name already removed — same table,
	// same ownership, one fewer declared field than the real table still
	// has (mirroring the real table created by createFixtureMigrationDDLTables,
	// which still has the column at the DB level even though the Go
	// declaration no longer mentions it).
	mc := NewModuleContext("req-1", "testmodule", "", "", nil, nil, slug, slug, "trace-1",
		abi.CapDBMigrationDDL, nil, ModuleSnapshot{
			ModelDecls: []model.ModelDeclaration{{
				Name: "widget",
				Fields: []model.NamedField{
					{Name: "id", Def: model.UUID().Required().PrimaryKey()},
					{Name: "tenant_id", Def: model.UUID().Required()},
				},
			}},
			OwnedModels: []string{"testmodule.widget"},
		})
	mc.IsDataMigrationJob = true

	_, hostErr := DBMigrationDDL(ctx, primaryDB, mc, dbMigrationDDLInput{
		Op: migrationDDLOpDropColumn, Table: "widget", Column: "legacy_name",
	})
	if hostErr != nil {
		t.Fatalf("DBMigrationDDL: %+v", hostErr)
	}
	if columnExists(t, primaryDB, slug, "widget", "legacy_name") {
		t.Error("legacy_name column still exists after dropping it via the documented remove-then-drop workflow")
	}
}

func TestDBMigrationDDL_RejectsInvalidIdentifiers(t *testing.T) {
	primaryDB, slug, mc := setupMigrationDDLTest(t)
	ctx := context.Background()

	_, hostErr := DBMigrationDDL(ctx, primaryDB, mc, dbMigrationDDLInput{
		Op: migrationDDLOpDropTable, Table: "widget; DROP TABLE gadget;--",
	})
	if hostErr == nil {
		t.Fatal("expected an error for a non-identifier table value")
	}
	if hostErr.Code != abi.ErrCodeMigrationDDLError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeMigrationDDLError)
	}
	if !tableExists(t, primaryDB, slug, "gadget") || !tableExists(t, primaryDB, slug, "widget") {
		t.Error("an injected statement affected the database")
	}
}

func TestDBMigrationDDL_RejectsUnknownOp(t *testing.T) {
	primaryDB, _, mc := setupMigrationDDLTest(t)
	ctx := context.Background()

	_, hostErr := DBMigrationDDL(ctx, primaryDB, mc, dbMigrationDDLInput{
		Op: "truncate_table", Table: "widget",
	})
	if hostErr == nil {
		t.Fatal("expected an error for an unknown op")
	}
	if hostErr.Code != abi.ErrCodeMigrationDDLError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeMigrationDDLError)
	}
}

// TestHostDBMigrationDDL_WiredThroughWASMBoundary is an end-to-end smoke
// test through the actual host.db.migration_ddl ABI registration — proving
// makeDBMigrationDDL's own capability and IsDataMigrationJob gates work,
// on top of DBMigrationDDL's much more thorough direct-call coverage
// above.
func TestHostDBMigrationDDL_WiredThroughWASMBoundary(t *testing.T) {
	primaryDB, slug, _ := setupMigrationDDLTest(t)
	ctx := context.Background()

	r := newHostDBTestRuntime(t, primaryDB, 10)

	newCaller := func(t *testing.T, mc *ModuleContext, name string) *ModuleInstance {
		t.Helper()
		caller := buildHostCallerModule("host.db", []string{"migration_ddl"})
		compiled, err := r.wazero.CompileModule(ctx, caller)
		if err != nil {
			t.Fatalf("CompileModule: %v", err)
		}
		t.Cleanup(func() { _ = compiled.Close(ctx) })
		inst, err := newModuleInstance(ctx, fmt.Sprintf("%s-%d", name, time.Now().UnixNano()), compiled, r.wazero)
		if err != nil {
			t.Fatalf("newModuleInstance: %v", err)
		}
		inst.SetModuleContext(mc)
		r.RegisterInstance(inst)
		t.Cleanup(func() { r.UnregisterInstance(inst) })
		return inst
	}

	t.Run("without db.migration_ddl capability", func(t *testing.T) {
		mc := NewModuleContext("req-1", "testmodule", "", "", nil, nil, slug, slug, "trace-1", 0, nil, ModuleSnapshot{
			ModelDecls:    []model.ModelDeclaration{migrationDDLWidgetModelDecl()},
			OwnedModels:   []string{"testmodule.widget"},
			ExtendsModels: nil,
		})
		mc.IsDataMigrationJob = true
		inst := newCaller(t, mc, "nocap")

		env := callHost(t, ctx, inst, "call_migration_ddl", dbMigrationDDLInput{Op: migrationDDLOpDropTable, Table: "widget"})
		if env.OK {
			t.Fatal("expected capability_denied, got success")
		}
		if env.Error.Code != abi.ErrCodeCapabilityDenied {
			t.Errorf("Code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
		}
	})

	t.Run("with capability but outside a data migration job", func(t *testing.T) {
		mc := newMigrationDDLTestModuleContext(slug, false)
		inst := newCaller(t, mc, "notmigration")

		env := callHost(t, ctx, inst, "call_migration_ddl", dbMigrationDDLInput{Op: migrationDDLOpDropTable, Table: "widget"})
		if env.OK {
			t.Fatal("expected db.migration_ddl_not_in_migration_context, got success")
		}
		if env.Error.Code != abi.ErrCodeMigrationDDLNotInContext {
			t.Errorf("Code = %q, want %q", env.Error.Code, abi.ErrCodeMigrationDDLNotInContext)
		}
	})

	t.Run("with capability inside a data migration job", func(t *testing.T) {
		mc := newMigrationDDLTestModuleContext(slug, true)
		inst := newCaller(t, mc, "migration")

		env := callHost(t, ctx, inst, "call_migration_ddl", dbMigrationDDLInput{
			Op: migrationDDLOpDropColumn, Table: "widget", Column: "legacy_name",
		})
		if !env.OK {
			t.Fatalf("migration_ddl failed: %+v", env.Error)
		}
		var out dbMigrationDDLOutput
		if err := msgpack.Unmarshal(env.Data, &out); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		if columnExists(t, primaryDB, slug, "widget", "legacy_name") {
			t.Error("legacy_name column still exists after DropColumn through the WASM boundary")
		}
	})
}
