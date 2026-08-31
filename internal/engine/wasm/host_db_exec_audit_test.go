package wasm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Reuses auditWidgetModelDecl/auditGadgetModelDecl/newAuditTestModuleContext
// and createFixtureAuditTables from host_orm_audit_test.go (same package,
// same fixture shape: a "testmodule" module with widget audited
// excluding "secret", and gadget unaudited).

func beginScopedTx(t *testing.T, ctx context.Context, db *sql.DB, mc *ModuleContext) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if err := applyTenantScope(ctx, tx, mc); err != nil {
		t.Fatalf("applyTenantScope: %v", err)
	}
	return tx
}

func mustParseAuditableExecStmt(t *testing.T, sqlText string) auditableExecStmt {
	t.Helper()
	tree, err := pg_query.Parse(sqlText)
	if err != nil {
		t.Fatalf("parse %q: %v", sqlText, err)
	}
	stmt, ok := parseAuditableExecStmt(tree)
	if !ok {
		t.Fatalf("parseAuditableExecStmt(%q): expected ok=true", sqlText)
	}
	return stmt
}

func TestParseAuditableExecStmt(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantOK    bool
		wantOp    string
		wantTable string
	}{
		{"update with where", "UPDATE widget SET name = $1 WHERE id = $2", true, "UPDATE", "widget"},
		{"delete with where", "DELETE FROM widget WHERE id = $1", true, "DELETE", "widget"},
		{"update without where", "UPDATE widget SET name = $1", true, "UPDATE", "widget"},
		{"insert not audited by this mechanism", "INSERT INTO widget (id) VALUES ($1)", false, "", ""},
		{"select rejected", "SELECT * FROM widget", false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, err := pg_query.Parse(tt.sql)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			stmt, ok := parseAuditableExecStmt(tree)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if stmt.Operation != tt.wantOp || stmt.Table != tt.wantTable {
				t.Errorf("got operation=%q table=%q, want operation=%q table=%q", stmt.Operation, stmt.Table, tt.wantOp, tt.wantTable)
			}
		})
	}
}

func TestParseAuditableExecStmt_MultipleStatements_RejectsAll(t *testing.T) {
	tree, err := pg_query.Parse("UPDATE widget SET name = 'a'; UPDATE widget SET name = 'b'")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := parseAuditableExecStmt(tree); ok {
		t.Fatal("expected ok=false for multiple statements")
	}
}

func TestResolveAuditedExecTable(t *testing.T) {
	mc := newAuditTestModuleContext("anytenant")

	if pkCol, excludeCols, audited := resolveAuditedExecTable(mc, "widget"); !audited || pkCol != "id" || !excludeCols["secret"] {
		t.Errorf("widget: audited=%v pkCol=%q excludeCols=%+v", audited, pkCol, excludeCols)
	}
	if _, _, audited := resolveAuditedExecTable(mc, "gadget"); audited {
		t.Error("gadget: expected not audited")
	}
	if _, _, audited := resolveAuditedExecTable(mc, "nonexistent_table"); audited {
		t.Error("nonexistent_table: expected not audited")
	}
}

func TestCaptureRowsBeforeExec_MatchesWhereClauseRows(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("execauditcap%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	mc := newAuditTestModuleContext(slug)
	tx := beginScopedTx(t, ctx, primaryDB, mc)

	if _, err := tx.ExecContext(ctx, `INSERT INTO widget (id, name, secret) VALUES
		('10000000-0000-0000-0000-000000000001', 'Widget A', 'shh1'),
		('10000000-0000-0000-0000-000000000002', 'Widget B', 'shh2'),
		('10000000-0000-0000-0000-000000000003', 'Widget C', 'shh3')`); err != nil {
		t.Fatalf("seed widgets: %v", err)
	}

	stmt := mustParseAuditableExecStmt(t, "UPDATE widget SET name = $1 WHERE name IN ($2, $3)")
	oldRows, err := captureRowsBeforeExec(ctx, tx, stmt, []any{"ignored", "Widget A", "Widget B"})
	if err != nil {
		t.Fatalf("captureRowsBeforeExec: %v", err)
	}
	if len(oldRows) != 2 {
		t.Fatalf("expected 2 rows captured, got %d: %+v", len(oldRows), oldRows)
	}
	names := map[string]bool{}
	for _, row := range oldRows {
		names[row["name"].(string)] = true
	}
	if !names["Widget A"] || !names["Widget B"] {
		t.Errorf("expected Widget A and Widget B captured, got %+v", oldRows)
	}
}

func TestCaptureRowsBeforeExec_TooFewParams_ReturnsError(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("execauditbadparams%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	mc := newAuditTestModuleContext(slug)
	tx := beginScopedTx(t, ctx, primaryDB, mc)

	stmt := mustParseAuditableExecStmt(t, "UPDATE widget SET name = $1 WHERE id = $2")
	if _, err := captureRowsBeforeExec(ctx, tx, stmt, []any{"only one value"}); err == nil {
		t.Fatal("expected an error for a WHERE clause $n beyond the supplied params, got nil")
	}
}

func TestCaptureRowsBeforeExec_NoWhereClause_CapturesAllRows(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("execauditcapall%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	mc := newAuditTestModuleContext(slug)
	tx := beginScopedTx(t, ctx, primaryDB, mc)

	if _, err := tx.ExecContext(ctx, `INSERT INTO widget (id, name, secret) VALUES
		('10000000-0000-0000-0000-000000000001', 'Widget A', 'shh1'),
		('10000000-0000-0000-0000-000000000002', 'Widget B', 'shh2')`); err != nil {
		t.Fatalf("seed widgets: %v", err)
	}

	stmt := mustParseAuditableExecStmt(t, "DELETE FROM widget")
	oldRows, err := captureRowsBeforeExec(ctx, tx, stmt, nil)
	if err != nil {
		t.Fatalf("captureRowsBeforeExec: %v", err)
	}
	if len(oldRows) != 2 {
		t.Fatalf("expected 2 rows captured, got %d: %+v", len(oldRows), oldRows)
	}
}

func TestWriteExecAuditEntries_Update_WritesOldAndNewData(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("execauditwrite%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	mc := newAuditTestModuleContext(slug)
	tx := beginScopedTx(t, ctx, primaryDB, mc)

	widgetID := "10000000-0000-0000-0000-000000000001"
	if _, err := tx.ExecContext(ctx, `INSERT INTO widget (id, name, secret) VALUES ($1, 'Widget A', 'shh')`, widgetID); err != nil {
		t.Fatalf("seed widget: %v", err)
	}

	stmt := mustParseAuditableExecStmt(t, "UPDATE widget SET name = $1 WHERE id = $2")
	oldRows, err := captureRowsBeforeExec(ctx, tx, stmt, []any{"Widget A Renamed", widgetID})
	if err != nil {
		t.Fatalf("captureRowsBeforeExec: %v", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE widget SET name = $1 WHERE id = $2`, "Widget A Renamed", widgetID); err != nil {
		t.Fatalf("exec update: %v", err)
	}
	newRows, err := scanRowsToMaps(mustQuery(t, ctx, tx, `SELECT * FROM widget WHERE id = $1`, widgetID))
	if err != nil {
		t.Fatalf("read new rows: %v", err)
	}

	pkCol, excludeCols, audited := resolveAuditedExecTable(mc, "widget")
	if !audited {
		t.Fatal("expected widget to be audited")
	}
	if err := writeExecAuditEntries(ctx, tx, mc, "widget", stmt, pkCol, excludeCols, oldRows, newRows); err != nil {
		t.Fatalf("writeExecAuditEntries: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit_log row, got %d: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Operation != "UPDATE" || row.RecordID != widgetID {
		t.Errorf("unexpected audit row: %+v", row)
	}
	var oldData, newData map[string]any
	if err := json.Unmarshal([]byte(row.OldData.String), &oldData); err != nil {
		t.Fatalf("unmarshal old_data: %v", err)
	}
	if err := json.Unmarshal([]byte(row.NewData.String), &newData); err != nil {
		t.Fatalf("unmarshal new_data: %v", err)
	}
	if oldData["name"] != "Widget A" {
		t.Errorf("old_data name = %v, want Widget A", oldData["name"])
	}
	if newData["name"] != "Widget A Renamed" {
		t.Errorf("new_data name = %v, want Widget A Renamed", newData["name"])
	}
	if _, ok := oldData["secret"]; ok {
		t.Errorf("expected secret excluded from old_data, got %+v", oldData)
	}
	if _, ok := newData["secret"]; ok {
		t.Errorf("expected secret excluded from new_data, got %+v", newData)
	}
}

func TestWriteExecAuditEntries_Delete_WritesOldDataOnly(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("execauditdelete%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	mc := newAuditTestModuleContext(slug)
	tx := beginScopedTx(t, ctx, primaryDB, mc)

	widgetID := "10000000-0000-0000-0000-000000000002"
	if _, err := tx.ExecContext(ctx, `INSERT INTO widget (id, name, secret) VALUES ($1, 'Widget B', 'shh')`, widgetID); err != nil {
		t.Fatalf("seed widget: %v", err)
	}

	stmt := mustParseAuditableExecStmt(t, "DELETE FROM widget WHERE id = $1")
	oldRows, err := captureRowsBeforeExec(ctx, tx, stmt, []any{widgetID})
	if err != nil {
		t.Fatalf("captureRowsBeforeExec: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM widget WHERE id = $1`, widgetID); err != nil {
		t.Fatalf("exec delete: %v", err)
	}

	pkCol, excludeCols, audited := resolveAuditedExecTable(mc, "widget")
	if !audited {
		t.Fatal("expected widget to be audited")
	}
	if err := writeExecAuditEntries(ctx, tx, mc, "widget", stmt, pkCol, excludeCols, oldRows, nil); err != nil {
		t.Fatalf("writeExecAuditEntries: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit_log row, got %d: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Operation != "DELETE" || row.RecordID != widgetID {
		t.Errorf("unexpected audit row: %+v", row)
	}
	if row.NewData.Valid {
		t.Errorf("expected new_data NULL for a delete, got %q", row.NewData.String)
	}
	var oldData map[string]any
	if err := json.Unmarshal([]byte(row.OldData.String), &oldData); err != nil {
		t.Fatalf("unmarshal old_data: %v", err)
	}
	if oldData["name"] != "Widget B" {
		t.Errorf("old_data name = %v, want Widget B", oldData["name"])
	}
}

func TestWriteExecAuditEntries_UpdateChangesPrimaryKey_WritesOneEntryNotTwo(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("execauditpkchange%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	mc := newAuditTestModuleContext(slug)
	tx := beginScopedTx(t, ctx, primaryDB, mc)

	oldID := "10000000-0000-0000-0000-000000000001"
	newID := "10000000-0000-0000-0000-000000000002"
	if _, err := tx.ExecContext(ctx, `INSERT INTO widget (id, name, secret) VALUES ($1, 'Widget A', 'shh')`, oldID); err != nil {
		t.Fatalf("seed widget: %v", err)
	}

	stmt := mustParseAuditableExecStmt(t, "UPDATE widget SET id = $1 WHERE id = $2")
	oldRows, err := captureRowsBeforeExec(ctx, tx, stmt, []any{newID, oldID})
	if err != nil {
		t.Fatalf("captureRowsBeforeExec: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE widget SET id = $1 WHERE id = $2`, newID, oldID); err != nil {
		t.Fatalf("exec update: %v", err)
	}
	newRows, err := scanRowsToMaps(mustQuery(t, ctx, tx, `SELECT * FROM widget WHERE id = $1`, newID))
	if err != nil {
		t.Fatalf("read new rows: %v", err)
	}

	pkCol, excludeCols, audited := resolveAuditedExecTable(mc, "widget")
	if !audited {
		t.Fatal("expected widget to be audited")
	}
	if err := writeExecAuditEntries(ctx, tx, mc, "widget", stmt, pkCol, excludeCols, oldRows, newRows); err != nil {
		t.Fatalf("writeExecAuditEntries: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit_log row for the primary-key-changing update, got %d: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Operation != "UPDATE" || row.RecordID != newID {
		t.Errorf("unexpected audit row: %+v", row)
	}
	if !row.OldData.Valid || !row.NewData.Valid {
		t.Fatalf("expected both old_data and new_data set, got %+v", row)
	}
	var oldData, newData map[string]any
	if err := json.Unmarshal([]byte(row.OldData.String), &oldData); err != nil {
		t.Fatalf("unmarshal old_data: %v", err)
	}
	if err := json.Unmarshal([]byte(row.NewData.String), &newData); err != nil {
		t.Fatalf("unmarshal new_data: %v", err)
	}
	if oldData["id"] != oldID || newData["id"] != newID {
		t.Errorf("old/new id mismatch: old=%v new=%v", oldData["id"], newData["id"])
	}
}

func TestWriteExecAuditEntries_MultiRowUpdate_WritesOneEntryPerRow(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("execauditmulti%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	mc := newAuditTestModuleContext(slug)
	tx := beginScopedTx(t, ctx, primaryDB, mc)

	if _, err := tx.ExecContext(ctx, `INSERT INTO widget (id, name, secret) VALUES
		('10000000-0000-0000-0000-000000000001', 'Widget A', 'shh1'),
		('10000000-0000-0000-0000-000000000002', 'Widget B', 'shh2')`); err != nil {
		t.Fatalf("seed widgets: %v", err)
	}

	stmt := mustParseAuditableExecStmt(t, "UPDATE widget SET name = name || $1")
	oldRows, err := captureRowsBeforeExec(ctx, tx, stmt, []any{" (updated)"})
	if err != nil {
		t.Fatalf("captureRowsBeforeExec: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE widget SET name = name || $1`, " (updated)"); err != nil {
		t.Fatalf("exec update: %v", err)
	}
	newRows, err := scanRowsToMaps(mustQuery(t, ctx, tx, `SELECT * FROM widget`))
	if err != nil {
		t.Fatalf("read new rows: %v", err)
	}

	pkCol, excludeCols, audited := resolveAuditedExecTable(mc, "widget")
	if !audited {
		t.Fatal("expected widget to be audited")
	}
	if err := writeExecAuditEntries(ctx, tx, mc, "widget", stmt, pkCol, excludeCols, oldRows, newRows); err != nil {
		t.Fatalf("writeExecAuditEntries: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit_log rows, got %d: %+v", len(rows), rows)
	}
}

func mustQuery(t *testing.T, ctx context.Context, tx *sql.Tx, sqlText string, args ...any) *sql.Rows {
	t.Helper()
	rows, err := tx.QueryContext(ctx, sqlText, args...)
	if err != nil {
		t.Fatalf("query %q: %v", sqlText, err)
	}
	return rows
}
