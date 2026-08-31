package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/dataaudit"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/vmihailenco/msgpack/v5"
)

// execWidgetModelDecl declares the standard etag/tenant_id columns plus
// name (unique — exercises db.unique_violation) and parent_id (FK into
// gadget — exercises db.foreign_key_violation), and is registered
// audited (secret excluded) by newExecTestModuleContext. gadget is
// declared but neither audited nor etag-bearing — the negative-path
// table for both mechanisms.
func execWidgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "widget",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "etag", Def: model.Text()},
			{Name: "name", Def: model.Text()},
			{Name: "secret", Def: model.Text()},
			{Name: "parent_id", Def: model.UUID()},
		},
	}
}

func execGadgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "gadget",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text()},
		},
	}
}

func createFixtureExecTables(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.gadget (
		id UUID PRIMARY KEY,
		name TEXT
	)`); err != nil {
		t.Fatalf("create gadget table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.widget (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		etag TEXT NOT NULL DEFAULT '',
		name TEXT UNIQUE,
		secret TEXT,
		parent_id UUID REFERENCES `+schemaName+`.gadget(id)
	)`); err != nil {
		t.Fatalf("create widget table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.audit_log (
		id          UUID NOT NULL DEFAULT uuidv7(),
		table_name  TEXT NOT NULL,
		record_id   UUID NOT NULL,
		operation   TEXT NOT NULL CHECK (operation IN ('INSERT','UPDATE','DELETE')),
		old_data    JSONB,
		new_data    JSONB,
		changed_by  UUID,
		changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		request_id  TEXT,
		trace_id    TEXT,
		PRIMARY KEY (id, changed_at)
	)`); err != nil {
		t.Fatalf("create audit_log table: %v", err)
	}
}

func newExecTestDataAuditRegistry() *dataaudit.Registry {
	reg := dataaudit.New()
	reg.Register("testmodule", []manifest.AuditedTable{
		{Table: "widget", ExcludeColumns: []string{"secret"}},
	}, []model.ModelDeclaration{execWidgetModelDecl(), execGadgetModelDecl()})
	return reg
}

func newExecTestModuleContext(tenantSlug string) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "00000000-0000-0000-0000-0000000000aa", "contact-1", []string{"admin"}, nil, tenantSlug, tenantSlug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:        []model.ModelDeclaration{execWidgetModelDecl(), execGadgetModelDecl()},
			DataAuditRegistry: newExecTestDataAuditRegistry(),
		})
}

func setupExecTest(t *testing.T) (*sql.DB, string, *ModuleContext) {
	t.Helper()
	primaryDB := openTestPrimaryDB(t)
	slug := fmt.Sprintf("dbexec%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureExecTables(t, primaryDB, slug)
	return primaryDB, slug, newExecTestModuleContext(slug)
}

func TestDBExec_Insert_Basic(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-000000000001"
	out, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		Params: []any{id, "Widget A"},
	})
	if hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}
	if out.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", out.RowsAffected)
	}
	if out.Returning != nil {
		t.Errorf("Returning = %v, want nil (opts.returning not set)", out.Returning)
	}
}

func TestDBExec_Insert_WithReturning(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-000000000002"
	out, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		Params: []any{id, "Widget B"},
		Opts:   dbExecOpts{Returning: "id, name"},
	})
	if hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}
	if len(out.Returning) != 1 || out.Returning[0][0] != id || out.Returning[0][1] != "Widget B" {
		t.Errorf("Returning = %v, want [[%s Widget B]]", out.Returning, id)
	}
}

// TestDBExec_Insert_WithReturning_UnknownColumn_ReturnsError is a
// regression test: a mistyped or nonexistent opts.returning column must
// error, not silently project to nil (indistinguishable from a real
// NULL value in the column's own actual position).
func TestDBExec_Insert_WithReturning_UnknownColumn_ReturnsError(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES (gen_random_uuid(), gen_random_uuid(), $1)",
		Params: []any{"Widget"},
		Opts:   dbExecOpts{Returning: "nmae"}, // typo for "name"
	})
	if hostErr == nil {
		t.Fatal("expected an error for a nonexistent opts.returning column")
	}
	if hostErr.Code != abi.ErrCodeExecError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeExecError)
	}
}

func TestDBExec_Update_Basic(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-000000000003"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "Original"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	out, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "UPDATE widget SET name = $1 WHERE id = $2", Params: []any{"Renamed", id},
	})
	if hostErr != nil {
		t.Fatalf("DBExec update: %+v", hostErr)
	}
	if out.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", out.RowsAffected)
	}
}

func TestDBExec_Delete_Basic(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-000000000004"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "ToDelete"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	out, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "DELETE FROM widget WHERE id = $1", Params: []any{id},
	})
	if hostErr != nil {
		t.Fatalf("DBExec delete: %+v", hostErr)
	}
	if out.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", out.RowsAffected)
	}
}

func TestDBExec_RejectsOwnReturningClause(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2) RETURNING id", Params: []any{"10000000-0000-0000-0000-000000000005", "X"},
	})
	if hostErr == nil {
		t.Fatal("expected an error for a statement with its own RETURNING clause")
	}
	if hostErr.Code != abi.ErrCodeExecError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeExecError)
	}
}

func TestDBExec_RejectsDDL(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{SQL: "ALTER TABLE widget ADD COLUMN evil TEXT"})
	if hostErr == nil {
		t.Fatal("expected an error for DDL")
	}
	if hostErr.Code != abi.ErrCodeExecError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeExecError)
	}
}

func TestDBExec_RejectsSelect(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{SQL: "SELECT * FROM widget"})
	if hostErr == nil {
		t.Fatal("expected an error for a SELECT")
	}
}

func TestDBExec_RejectsMultipleStatements(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{SQL: "DELETE FROM widget; DELETE FROM gadget;"})
	if hostErr == nil {
		t.Fatal("expected an error for multiple statements")
	}
}

func TestDBExec_RejectsQualifiedTableReference(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{SQL: "DELETE FROM tenant_" + slug + ".widget"})
	if hostErr == nil {
		t.Fatal("expected an error for a schema-qualified table reference")
	}
	if hostErr.Code != abi.ErrCodeTableAccessDenied {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeTableAccessDenied)
	}
}

func TestDBExec_RejectsReturningStar(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:  "INSERT INTO widget (id, tenant_id) VALUES (gen_random_uuid(), gen_random_uuid())",
		Opts: dbExecOpts{Returning: "*"},
	})
	if hostErr == nil {
		t.Fatal("expected an error for opts.returning = \"*\"")
	}
}

func TestDBExec_UniqueViolation(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		Params: []any{"10000000-0000-0000-0000-000000000006", "Dup"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		Params: []any{"10000000-0000-0000-0000-000000000007", "Dup"},
	})
	if hostErr == nil {
		t.Fatal("expected a unique violation")
	}
	if hostErr.Code != abi.ErrCodeDBUniqueViolation {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeDBUniqueViolation)
	}
}

func TestDBExec_ForeignKeyViolation(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, parent_id) VALUES (gen_random_uuid(), gen_random_uuid(), $1)",
		Params: []any{"20000000-0000-0000-0000-000000000001"},
	})
	if hostErr == nil {
		t.Fatal("expected a foreign key violation")
	}
	if hostErr.Code != abi.ErrCodeDBForeignKeyViolation {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeDBForeignKeyViolation)
	}
	if hostErr.Details["table"] != "widget" || hostErr.Details["column"] != "parent_id" {
		t.Errorf("Details = %+v, want table=widget column=parent_id", hostErr.Details)
	}
}

// TestDBExec_UniqueViolation_IncludesSQLState and
// TestDBExec_Deadlock_SurfacesSQLState cover translateExecError's
// "sqlstate" Details field, added for sdk/go/db.PGError (goerp#509).
func TestDBExec_UniqueViolation_IncludesSQLState(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		Params: []any{"30000000-0000-0000-0000-000000000001", "SQLStateDup"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		Params: []any{"30000000-0000-0000-0000-000000000002", "SQLStateDup"},
	})
	if hostErr == nil {
		t.Fatal("expected a unique violation")
	}
	if hostErr.Details["sqlstate"] != "23505" {
		t.Errorf("Details[sqlstate] = %v, want %q", hostErr.Details["sqlstate"], "23505")
	}
}

// TestDBExec_Deadlock_SurfacesSQLState triggers a real Postgres deadlock
// (opposite lock order on the same two rows, in two caller-owned
// transactions) and confirms the aborted side's db.exec_error carries
// Details["sqlstate"] == "40P01".
func TestDBExec_Deadlock_SurfacesSQLState(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	id1 := "30000000-0000-0000-0000-000000000003"
	id2 := "30000000-0000-0000-0000-000000000004"
	for _, id := range []string{id1, id2} {
		if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "Row " + id},
		}); hostErr != nil {
			t.Fatalf("seed insert %s: %+v", id, hostErr)
		}
	}

	beginTx := func(txID string) *sql.Tx {
		tx, err := primaryDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin %s: %v", txID, err)
		}
		if err := applyTenantScope(ctx, tx, mc); err != nil {
			t.Fatalf("applyTenantScope %s: %v", txID, err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		mc.RegisterTransaction(txID, tx)
		return tx
	}
	tx1 := beginTx("deadlock-tx1")
	tx2 := beginTx("deadlock-tx2")

	// Rendezvous barrier, not a timing guess: a time.Sleep-based version
	// raced, since the faster goroutine could finish entirely before the
	// slower one even started, leaving wg.Wait() blocked on a lock only
	// an unrolled-back transaction held.
	var firstLockDone sync.WaitGroup
	firstLockDone.Add(2)

	var wg sync.WaitGroup
	errs := make([]*abi.HostError, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL: "UPDATE widget SET name = $1 WHERE id = $2", Params: []any{"A1", id1}, TxID: "deadlock-tx1",
		}); hostErr != nil {
			errs[0] = hostErr
			firstLockDone.Done()
			return
		}
		firstLockDone.Done()
		firstLockDone.Wait()
		_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL: "UPDATE widget SET name = $1 WHERE id = $2", Params: []any{"A2", id2}, TxID: "deadlock-tx1",
		})
		errs[0] = hostErr
	}()
	go func() {
		defer wg.Done()
		if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL: "UPDATE widget SET name = $1 WHERE id = $2", Params: []any{"B1", id2}, TxID: "deadlock-tx2",
		}); hostErr != nil {
			errs[1] = hostErr
			firstLockDone.Done()
			return
		}
		firstLockDone.Done()
		firstLockDone.Wait()
		_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL: "UPDATE widget SET name = $1 WHERE id = $2", Params: []any{"B2", id1}, TxID: "deadlock-tx2",
		})
		errs[1] = hostErr
	}()
	wg.Wait()

	_ = tx1.Rollback()
	_ = tx2.Rollback()

	var deadlockErr *abi.HostError
	survivorIdx := -1
	for i, e := range errs {
		if e != nil && e.Details["sqlstate"] == "40P01" {
			deadlockErr = e
			survivorIdx = 1 - i
		}
	}
	if deadlockErr == nil {
		t.Fatalf("expected one side to report a deadlock (sqlstate 40P01); got errs = %+v, %+v", errs[0], errs[1])
	}
	if deadlockErr.Code != abi.ErrCodeExecError {
		t.Errorf("Code = %q, want %q", deadlockErr.Code, abi.ErrCodeExecError)
	}
	if errs[survivorIdx] != nil {
		t.Errorf("the non-deadlocked side should have succeeded, got: %+v", errs[survivorIdx])
	}
}

func TestDBExec_EtagMismatch(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-000000000008"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "Etagged"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "UPDATE widget SET name = $1 WHERE id = $2 AND etag = $3",
		Params: []any{"Renamed", id, "stale-etag-value"},
	})
	if hostErr == nil {
		t.Fatal("expected an etag mismatch")
	}
	if hostErr.Code != abi.ErrCodeDBEtagMismatch {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeDBEtagMismatch)
	}
}

func TestDBExec_EtagCheck_MatchingEtagSucceeds(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-000000000009"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "Etagged"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	out, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "UPDATE widget SET name = $1 WHERE id = $2 AND etag = $3",
		Params: []any{"Renamed", id, ""}, // etag column defaults to '' and is never rotated without the #455 trigger installed in this fixture
	})
	if hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}
	if out.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", out.RowsAffected)
	}
}

func TestDBExec_SkipEtag_BypassesMismatch(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-00000000000a"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "Etagged"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	out, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "UPDATE widget SET name = $1 WHERE id = $2 AND etag = $3",
		Params: []any{"Renamed", id, "stale-etag-value"},
		Opts:   dbExecOpts{SkipEtag: true},
	})
	if hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}
	if out.RowsAffected != 0 {
		t.Errorf("RowsAffected = %d, want 0 (skip_etag still runs the statement, which matches nothing)", out.RowsAffected)
	}
}

func TestDBExec_ExpectRows_ZeroRowsReturnsError(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "UPDATE gadget SET name = $1 WHERE id = $2",
		Params: []any{"x", "10000000-0000-0000-0000-00000000000b"},
		Opts:   dbExecOpts{ExpectRows: true},
	})
	if hostErr == nil {
		t.Fatal("expected db.no_rows_affected")
	}
	if hostErr.Code != abi.ErrCodeNoRowsAffected {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeNoRowsAffected)
	}
}

func TestDBExec_NoExpectRows_ZeroRowsIsNotAnError(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	out, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "UPDATE gadget SET name = $1 WHERE id = $2",
		Params: []any{"x", "10000000-0000-0000-0000-00000000000c"},
	})
	if hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}
	if out.RowsAffected != 0 {
		t.Errorf("RowsAffected = %d, want 0", out.RowsAffected)
	}
}

func TestDBExec_Audit_InsertWritesAuditLogRow(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-00000000000d"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name, secret) VALUES ($1, gen_random_uuid(), $2, $3)",
		Params: []any{id, "Audited Insert", "shh"},
	}); hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit_log row, got %d: %+v", len(rows), rows)
	}
	if rows[0].Operation != "INSERT" || rows[0].RecordID != id {
		t.Errorf("unexpected audit row: %+v", rows[0])
	}
	if rows[0].OldData.Valid {
		t.Errorf("expected old_data NULL for an insert, got %q", rows[0].OldData.String)
	}
}

func TestDBExec_Audit_UpdateWritesOldAndNewData(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-00000000000e"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "Before"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "UPDATE widget SET name = $1 WHERE id = $2", Params: []any{"After", id},
	}); hostErr != nil {
		t.Fatalf("DBExec update: %+v", hostErr)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit_log rows (insert + update), got %d: %+v", len(rows), rows)
	}
	if rows[1].Operation != "UPDATE" {
		t.Errorf("operation = %q, want UPDATE", rows[1].Operation)
	}
}

func TestDBExec_Audit_DeleteWritesOldDataOnly(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-00000000000f"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "ToDelete"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "DELETE FROM widget WHERE id = $1", Params: []any{id},
	}); hostErr != nil {
		t.Fatalf("DBExec delete: %+v", hostErr)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit_log rows (insert + delete), got %d: %+v", len(rows), rows)
	}
	if rows[1].Operation != "DELETE" || rows[1].NewData.Valid {
		t.Errorf("unexpected delete audit row: %+v", rows[1])
	}
}

// TestDBExec_Audit_DeleteWithOptsReturning_NewDataStaysNull is a
// regression test: a DELETE's own RETURNING output (when the module
// itself sets opts.returning on a DELETE) reflects each row's last
// values before removal, not "new" state — writeAuditForExec must not
// let those values leak into the audit entry's new_data.
func TestDBExec_Audit_DeleteWithOptsReturning_NewDataStaysNull(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	id := "10000000-0000-0000-0000-000000000012"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "ToDeleteWithReturning"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	out, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "DELETE FROM widget WHERE id = $1", Params: []any{id},
		Opts: dbExecOpts{Returning: "id, name"},
	})
	if hostErr != nil {
		t.Fatalf("DBExec delete: %+v", hostErr)
	}
	if len(out.Returning) != 1 || out.Returning[0][0] != id {
		t.Errorf("Returning = %v, want the deleted row's id/name — opts.returning on a DELETE must still work", out.Returning)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit_log rows, got %d: %+v", len(rows), rows)
	}
	if rows[1].Operation != "DELETE" || rows[1].NewData.Valid {
		t.Errorf("unexpected delete audit row: %+v", rows[1])
	}
}

func TestDBExec_SkipAudit_NoAuditLogRow(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES (gen_random_uuid(), gen_random_uuid(), $1)",
		Params: []any{"Unaudited"},
		Opts:   dbExecOpts{SkipAudit: true},
	}); hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}

	if rows := queryAuditLogRows(t, primaryDB, slug, "widget"); len(rows) != 0 {
		t.Fatalf("expected no audit_log rows with skip_audit, got %+v", rows)
	}
}

func TestDBExec_UnauditedTable_NoAuditLogRow(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO gadget (id, name) VALUES (gen_random_uuid(), $1)",
		Params: []any{"Gadget A"},
	}); hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}

	if rows := queryAuditLogRows(t, primaryDB, slug, "gadget"); len(rows) != 0 {
		t.Fatalf("expected no audit_log rows for an unaudited table, got %+v", rows)
	}
}

func TestDBExec_BorrowedTransaction_NotAutoCommitted(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	tx, err := primaryDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := applyTenantScope(ctx, tx, mc); err != nil {
		t.Fatalf("applyTenantScope: %v", err)
	}
	txID := "test-tx-1"
	mc.RegisterTransaction(txID, tx)

	id := "10000000-0000-0000-0000-000000000010"
	out, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "InTx"},
		TxID: txID,
	})
	if hostErr != nil {
		t.Fatalf("DBExec: %+v", hostErr)
	}
	if out.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", out.RowsAffected)
	}

	// Not yet committed — a separate connection must not see the row.
	var count int
	if err := primaryDB.QueryRow("SELECT count(*) FROM tenant_"+mc.TenantSlug+".widget WHERE id = $1", id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("row visible before commit: count = %d, want 0", count)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := primaryDB.QueryRow("SELECT count(*) FROM tenant_"+mc.TenantSlug+".widget WHERE id = $1", id).Scan(&count); err != nil {
		t.Fatalf("count after commit: %v", err)
	}
	if count != 1 {
		t.Errorf("row not visible after commit: count = %d, want 1", count)
	}
}

func TestDBExec_UnknownTransactionID(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "DELETE FROM widget WHERE id = $1", Params: []any{"10000000-0000-0000-0000-000000000011"},
		TxID: "does-not-exist",
	})
	if hostErr == nil {
		t.Fatal("expected an error for an unknown tx_id")
	}
	if hostErr.Code != abi.ErrCodeTransactionNotFound {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeTransactionNotFound)
	}
}

// TestHostDBExec_WiredThroughWASMBoundary is an end-to-end smoke test
// through the actual host.db.exec ABI registration — proving makeDBExec
// marshals/unmarshals correctly and the capability gate works, on top of
// DBExec's own much more thorough direct-call coverage above.
func TestHostDBExec_WiredThroughWASMBoundary(t *testing.T) {
	primaryDB, slug, _ := setupExecTest(t)
	ctx := context.Background()

	r := newHostDBTestRuntime(t, primaryDB, 10)

	t.Run("without db.write capability", func(t *testing.T) {
		mc := newTestModuleContext(slug, abi.CapDBRead, r.TxLimiter())
		caller := buildHostCallerModule("host.db", []string{"begin", "commit", "rollback", "query", "query_replica", "exec"})
		compiled, err := r.wazero.CompileModule(ctx, caller)
		if err != nil {
			t.Fatalf("CompileModule: %v", err)
		}
		t.Cleanup(func() { _ = compiled.Close(ctx) })
		inst, err := newModuleInstance(ctx, fmt.Sprintf("exec-caller-nocap-%d", time.Now().UnixNano()), compiled, r.wazero)
		if err != nil {
			t.Fatalf("newModuleInstance: %v", err)
		}
		inst.SetModuleContext(mc)
		r.RegisterInstance(inst)
		t.Cleanup(func() { r.UnregisterInstance(inst) })

		env := callHost(t, ctx, inst, "call_exec", dbExecInput{SQL: "DELETE FROM widget WHERE id = $1", Params: []any{"x"}})
		if env.OK {
			t.Fatal("expected capability_denied, got success")
		}
		if env.Error.Code != abi.ErrCodeCapabilityDenied {
			t.Errorf("Code = %q, want %q", env.Error.Code, abi.ErrCodeCapabilityDenied)
		}
	})

	t.Run("with db.write capability", func(t *testing.T) {
		mc := newTestModuleContext(slug, abi.CapDBRead|abi.CapDBWrite, r.TxLimiter())
		caller := buildHostCallerModule("host.db", []string{"begin", "commit", "rollback", "query", "query_replica", "exec"})
		compiled, err := r.wazero.CompileModule(ctx, caller)
		if err != nil {
			t.Fatalf("CompileModule: %v", err)
		}
		t.Cleanup(func() { _ = compiled.Close(ctx) })
		inst, err := newModuleInstance(ctx, fmt.Sprintf("exec-caller-cap-%d", time.Now().UnixNano()), compiled, r.wazero)
		if err != nil {
			t.Fatalf("newModuleInstance: %v", err)
		}
		inst.SetModuleContext(mc)
		r.RegisterInstance(inst)
		t.Cleanup(func() { r.UnregisterInstance(inst) })

		env := callHost(t, ctx, inst, "call_exec", dbExecInput{
			SQL: "INSERT INTO widget (id, tenant_id, name) VALUES (gen_random_uuid(), gen_random_uuid(), $1)", Params: []any{"WASM Widget"},
		})
		if !env.OK {
			t.Fatalf("exec failed: %+v", env.Error)
		}
		var out dbExecOutput
		if err := msgpack.Unmarshal(env.Data, &out); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		if out.RowsAffected != 1 {
			t.Errorf("RowsAffected = %d, want 1", out.RowsAffected)
		}
	})
}
