package wasm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/dataaudit"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// auditWidgetModelDecl and auditGadgetModelDecl exercise the audit-log
// write path (goerp#363): widget is declared in the fixture module's own
// audited_tables[] (with "secret" excluded), gadget is not — proving the
// no-op path never inserts an audit_log row for an unaudited table.
func auditWidgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "widget",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text()},
			{Name: "secret", Def: model.Text()},
		},
	}
}

func auditGadgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "gadget",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text()},
		},
	}
}

func newAuditTestDataAuditRegistry() *dataaudit.Registry {
	reg := dataaudit.New()
	reg.Register("testmodule", []manifest.AuditedTable{
		{Table: "widget", ExcludeColumns: []string{"secret"}},
	}, []model.ModelDeclaration{auditWidgetModelDecl(), auditGadgetModelDecl()})
	return reg
}

// createFixtureAuditTables creates widgets/gadgets tables plus a
// same-shape stand-in for the real engine-owned audit_log table
// (internal/engine/tenant/provision/activities.go's createAuditLogTable)
// — a local copy rather than importing that package, matching every
// other fixture table in this file being self-contained DDL.
func createFixtureAuditTables(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.widget (
		id UUID PRIMARY KEY,
		name TEXT,
		secret TEXT
	)`); err != nil {
		t.Fatalf("create widget table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.gadget (
		id UUID PRIMARY KEY,
		name TEXT
	)`); err != nil {
		t.Fatalf("create gadget table: %v", err)
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

type auditLogRow struct {
	TableName string
	RecordID  string
	Operation string
	OldData   sql.NullString
	NewData   sql.NullString
}

func queryAuditLogRows(t *testing.T, conn *sql.DB, slug, tableName string) []auditLogRow {
	t.Helper()
	rows, err := conn.Query(`SELECT table_name, record_id, operation, old_data, new_data FROM tenant_`+slug+`.audit_log WHERE table_name = $1 ORDER BY changed_at`, tableName)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	defer rows.Close()

	var out []auditLogRow
	for rows.Next() {
		var r auditLogRow
		if err := rows.Scan(&r.TableName, &r.RecordID, &r.Operation, &r.OldData, &r.NewData); err != nil {
			t.Fatalf("scan audit_log row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit_log rows: %v", err)
	}
	return out
}

func newAuditTestModuleContext(tenantSlug string) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "00000000-0000-0000-0000-0000000000aa", "contact-1", []string{"admin"}, nil, tenantSlug, tenantSlug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:        []model.ModelDeclaration{auditWidgetModelDecl(), auditGadgetModelDecl()},
			DataAuditRegistry: newAuditTestDataAuditRegistry(),
		})
}

func TestORMCreate_AuditedTable_WritesInsertRow(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("auditcreate%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newAuditTestModuleContext(slug)
	insertClient := r.EventInsertClient()

	widgetID := "10000000-0000-0000-0000-000000000001"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.widget",
		Record: map[string]any{"id": widgetID, "name": "Widget A", "secret": "shh"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit_log row, got %d: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Operation != "INSERT" || row.RecordID != widgetID {
		t.Errorf("unexpected audit row: %+v", row)
	}
	if row.OldData.Valid {
		t.Errorf("expected old_data NULL for a create, got %q", row.OldData.String)
	}
	if !row.NewData.Valid {
		t.Fatal("expected new_data to be set")
	}
	var newData map[string]any
	if err := json.Unmarshal([]byte(row.NewData.String), &newData); err != nil {
		t.Fatalf("unmarshal new_data: %v", err)
	}
	if _, ok := newData["secret"]; ok {
		t.Errorf("expected secret excluded from new_data, got %+v", newData)
	}
	if newData["name"] != "Widget A" {
		t.Errorf("new_data name = %v, want Widget A", newData["name"])
	}
}

func TestORMWrite_AuditedTable_WritesUpdateRowWithOldAndNewData(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("auditwrite%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newAuditTestModuleContext(slug)
	insertClient := r.EventInsertClient()

	widgetID := "10000000-0000-0000-0000-000000000002"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.widget",
		Record: map[string]any{"id": widgetID, "name": "Widget B", "secret": "shh"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	if _, hostErr := ORMWrite(ctx, r, primaryDB, insertClient, nil, mc, ORMWriteInput{
		Model:  "testmodule.widget",
		ID:     widgetID,
		Record: map[string]any{"name": "Widget B Renamed"},
	}); hostErr != nil {
		t.Fatalf("ORMWrite: %+v", hostErr)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit_log rows (create + write), got %d: %+v", len(rows), rows)
	}
	writeRow := rows[1]
	if writeRow.Operation != "UPDATE" {
		t.Errorf("operation = %q, want UPDATE", writeRow.Operation)
	}
	var oldData, newData map[string]any
	if err := json.Unmarshal([]byte(writeRow.OldData.String), &oldData); err != nil {
		t.Fatalf("unmarshal old_data: %v", err)
	}
	if err := json.Unmarshal([]byte(writeRow.NewData.String), &newData); err != nil {
		t.Fatalf("unmarshal new_data: %v", err)
	}
	if oldData["name"] != "Widget B" {
		t.Errorf("old_data name = %v, want Widget B", oldData["name"])
	}
	if newData["name"] != "Widget B Renamed" {
		t.Errorf("new_data name = %v, want Widget B Renamed", newData["name"])
	}
}

func TestORMUnlink_AuditedTable_WritesDeleteRowWithOldData(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("auditunlink%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newAuditTestModuleContext(slug)
	insertClient := r.EventInsertClient()

	widgetID := "10000000-0000-0000-0000-000000000003"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.widget",
		Record: map[string]any{"id": widgetID, "name": "Widget C", "secret": "shh"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	if _, hostErr := ORMUnlink(ctx, r, primaryDB, insertClient, nil, mc, ORMUnlinkInput{
		Model: "testmodule.widget",
		ID:    widgetID,
	}); hostErr != nil {
		t.Fatalf("ORMUnlink: %+v", hostErr)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit_log rows (create + delete), got %d: %+v", len(rows), rows)
	}
	deleteRow := rows[1]
	if deleteRow.Operation != "DELETE" {
		t.Errorf("operation = %q, want DELETE", deleteRow.Operation)
	}
	if deleteRow.NewData.Valid {
		t.Errorf("expected new_data NULL for a delete, got %q", deleteRow.NewData.String)
	}
	var oldData map[string]any
	if err := json.Unmarshal([]byte(deleteRow.OldData.String), &oldData); err != nil {
		t.Fatalf("unmarshal old_data: %v", err)
	}
	if oldData["name"] != "Widget C" {
		t.Errorf("old_data name = %v, want Widget C", oldData["name"])
	}
}

func TestORMCreate_UnauditedTable_NoAuditLogRow(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("auditnoop%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureAuditTables(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newAuditTestModuleContext(slug)
	insertClient := r.EventInsertClient()

	gadgetID := "10000000-0000-0000-0000-000000000004"
	if _, hostErr := ORMCreate(ctx, r, primaryDB, insertClient, nil, mc, ORMCreateInput{
		Model:  "testmodule.gadget",
		Record: map[string]any{"id": gadgetID, "name": "Gadget A"},
	}); hostErr != nil {
		t.Fatalf("ORMCreate: %+v", hostErr)
	}

	if rows := queryAuditLogRows(t, primaryDB, slug, "gadget"); len(rows) != 0 {
		t.Fatalf("expected no audit_log rows for an unaudited table, got %+v", rows)
	}
}
