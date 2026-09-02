package schema

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func etagWidgetModel() model.ModelDeclaration {
	return *model.Define("etagtest.widget", model.Table("etag_widgets")).
		WithStandardFields().
		Field("name", model.Text())
}

func etagGadgetModel() model.ModelDeclaration {
	return *model.Define("etagtest.gadget", model.Table("etag_gadgets")).
		WithStandardFields().
		Field("name", model.Text())
}

// etagTriggerExists reports whether table carries a live
// {table}_etag_trigger, via the same pg_trigger catalog query this
// file's own assertions already use.
func etagTriggerExists(t *testing.T, conn *sql.DB, schemaName, table string) bool {
	t.Helper()
	var count int
	if err := conn.QueryRow(
		"SELECT count(*) FROM pg_trigger t JOIN pg_class c ON t.tgrelid = c.oid JOIN pg_namespace n ON c.relnamespace = n.oid WHERE n.nspname = $1 AND c.relname = $2 AND t.tgname = $3",
		schemaName, table, table+"_etag_trigger",
	).Scan(&count); err != nil {
		t.Fatalf("count triggers: %v", err)
	}
	return count > 0
}

// updateEtagFunctionExists reports whether the shared update_etag()
// function still exists in schemaName — reconciliation must never drop
// it, since every module's etag triggers there call it.
func updateEtagFunctionExists(t *testing.T, conn *sql.DB, schemaName string) bool {
	t.Helper()
	var count int
	if err := conn.QueryRow(
		"SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON p.pronamespace = n.oid WHERE n.nspname = $1 AND p.proname = 'update_etag'",
		schemaName,
	).Scan(&count); err != nil {
		t.Fatalf("count update_etag function: %v", err)
	}
	return count > 0
}

func createAndSyncEtagWidget(t *testing.T, sess *SchemaSyncSession, engine *SchemaDiffEngine, modelDecls []model.ModelDeclaration, auditedTables []manifest.AuditedTable) {
	t.Helper()

	changes, err := engine.Diff(context.Background(), sess, modelDecls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, modelDecls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if err := engine.SyncEtagTriggers(context.Background(), sess, modelDecls, auditedTables); err != nil {
		t.Fatalf("SyncEtagTriggers() error: %v", err)
	}
}

func etagAndUpdatedAt(t *testing.T, conn *sql.DB, table, id string) (etag string, updatedAt time.Time) {
	t.Helper()
	if err := conn.QueryRow("SELECT etag, updated_at FROM "+table+" WHERE id = $1", id).Scan(&etag, &updatedAt); err != nil {
		t.Fatalf("query etag/updated_at: %v", err)
	}
	return etag, updatedAt
}

func TestSyncEtagTriggers_UpdateComputesEtagAndUpdatedAt(t *testing.T) {
	slug := "etagtriggertest"
	sess, engine := setupTenantSchema(t, slug)
	adminConn, _ := openTestPool(t, 5*time.Second)

	modelDecls := []model.ModelDeclaration{etagWidgetModel()}
	auditedTables := []manifest.AuditedTable{{Table: "etag_widgets"}}
	createAndSyncEtagWidget(t, sess, engine, modelDecls, auditedTables)

	table := quoteIdent("tenant_"+slug) + "." + quoteIdent("etag_widgets")
	id := "10000000-0000-0000-0000-000000000001"
	if _, err := adminConn.Exec("INSERT INTO "+table+" (id, tenant_id, name) VALUES ($1, gen_random_uuid(), 'Widget A')", id); err != nil {
		t.Fatalf("insert row: %v", err)
	}

	// etag's own column default ('') is untouched by INSERT — the
	// trigger is BEFORE UPDATE only, matching data-layer.md §2.4.
	etagBefore, updatedAtBefore := etagAndUpdatedAt(t, adminConn, table, id)
	if etagBefore != "" {
		t.Fatalf("etag before any update = %q, want the column default ''", etagBefore)
	}

	if _, err := adminConn.Exec("UPDATE "+table+" SET name = $1 WHERE id = $2", "Widget A Renamed", id); err != nil {
		t.Fatalf("update row: %v", err)
	}

	etagAfter, updatedAtAfter := etagAndUpdatedAt(t, adminConn, table, id)
	if etagAfter == "" || etagAfter == etagBefore {
		t.Errorf("etag after update = %q (before %q), want a new non-empty hash", etagAfter, etagBefore)
	}
	if !updatedAtAfter.After(updatedAtBefore) {
		t.Errorf("updated_at after update (%v) is not after before (%v)", updatedAtAfter, updatedAtBefore)
	}
}

func TestSyncEtagTriggers_HashExcludesIdTenantIdEtagUpdatedAtCreatedAt(t *testing.T) {
	slug := "etagtriggerhashtest"
	sess, engine := setupTenantSchema(t, slug)
	adminConn, _ := openTestPool(t, 5*time.Second)

	modelDecls := []model.ModelDeclaration{etagWidgetModel()}
	auditedTables := []manifest.AuditedTable{{Table: "etag_widgets"}}
	createAndSyncEtagWidget(t, sess, engine, modelDecls, auditedTables)

	table := quoteIdent("tenant_"+slug) + "." + quoteIdent("etag_widgets")
	idA := "10000000-0000-0000-0000-000000000002"
	idB := "10000000-0000-0000-0000-000000000003"
	if _, err := adminConn.Exec("INSERT INTO "+table+" (id, tenant_id, name) VALUES ($1, gen_random_uuid(), 'same name')", idA); err != nil {
		t.Fatalf("insert row A: %v", err)
	}
	if _, err := adminConn.Exec("INSERT INTO "+table+" (id, tenant_id, name) VALUES ($1, gen_random_uuid(), 'different')", idB); err != nil {
		t.Fatalf("insert row B: %v", err)
	}

	// Two rows with different id/tenant_id/created_at/etag/updated_at but
	// the same domain field value (name), updated to the same domain
	// field value, must hash to the same etag — proving the trigger
	// excludes the identity/bookkeeping columns from the hash rather than
	// accidentally including them.
	if _, err := adminConn.Exec("UPDATE "+table+" SET name = $1 WHERE id = $2", "converged", idA); err != nil {
		t.Fatalf("update row A: %v", err)
	}
	if _, err := adminConn.Exec("UPDATE "+table+" SET name = $1 WHERE id = $2", "converged", idB); err != nil {
		t.Fatalf("update row B: %v", err)
	}

	etagA, _ := etagAndUpdatedAt(t, adminConn, table, idA)
	etagB, _ := etagAndUpdatedAt(t, adminConn, table, idB)
	if etagA != etagB {
		t.Errorf("etagA = %q, etagB = %q, want equal hashes for identical domain content", etagA, etagB)
	}
}

func TestSyncEtagTriggers_NoAuditedTables_InstallsNoTrigger(t *testing.T) {
	slug := "etagtriggernoop"
	sess, engine := setupTenantSchema(t, slug)
	adminConn, _ := openTestPool(t, 5*time.Second)

	modelDecls := []model.ModelDeclaration{etagWidgetModel()}
	createAndSyncEtagWidget(t, sess, engine, modelDecls, nil)

	var count int
	if err := adminConn.QueryRow(
		"SELECT count(*) FROM pg_trigger t JOIN pg_class c ON t.tgrelid = c.oid JOIN pg_namespace n ON c.relnamespace = n.oid WHERE n.nspname = $1 AND c.relname = 'etag_widgets'",
		"tenant_"+slug,
	).Scan(&count); err != nil {
		t.Fatalf("count triggers: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no trigger installed for a module with no audited_tables, found %d", count)
	}
}

func TestSyncEtagTriggers_UnmatchedTableName_ReturnsError(t *testing.T) {
	slug := "etagtriggerunmatched"
	sess, engine := setupTenantSchema(t, slug)

	modelDecls := []model.ModelDeclaration{etagWidgetModel()}
	changes, err := engine.Diff(context.Background(), sess, modelDecls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, modelDecls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	auditedTables := []manifest.AuditedTable{{Table: "nonexistent_table"}}
	if err := engine.SyncEtagTriggers(context.Background(), sess, modelDecls, auditedTables); err == nil {
		t.Fatal("expected an error for an audited_tables entry naming a table no declared model owns")
	}
}

func TestSyncEtagTriggers_ModelMissingEtagColumn_ReturnsError(t *testing.T) {
	slug := "etagtriggernoetagcol"
	sess, engine := setupTenantSchema(t, slug)

	bareModel := *model.Define("etagtest.bare", model.Table("bare_widgets")).
		Field("id", model.UUID().Required().PrimaryKey()).
		Field("name", model.Text())
	modelDecls := []model.ModelDeclaration{bareModel}

	changes, err := engine.Diff(context.Background(), sess, modelDecls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, modelDecls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	auditedTables := []manifest.AuditedTable{{Table: "bare_widgets"}}
	if err := engine.SyncEtagTriggers(context.Background(), sess, modelDecls, auditedTables); err == nil {
		t.Fatal("expected an error for an audited table whose model has no etag column")
	}
}

func TestSyncEtagTriggers_ReSync_Idempotent(t *testing.T) {
	slug := "etagtriggerresync"
	sess, engine := setupTenantSchema(t, slug)

	modelDecls := []model.ModelDeclaration{etagWidgetModel()}
	auditedTables := []manifest.AuditedTable{{Table: "etag_widgets"}}
	createAndSyncEtagWidget(t, sess, engine, modelDecls, auditedTables)

	if err := engine.SyncEtagTriggers(context.Background(), sess, modelDecls, auditedTables); err != nil {
		t.Fatalf("second SyncEtagTriggers() call error: %v", err)
	}
}

// Removing a table from audited_tables[] (module stays installed) drops
// that table's etag trigger on the next sync, leaving any other audited
// table's trigger untouched — goerp#563 AC.
func TestSyncEtagTriggers_Reconciliation_RemovingOneTableKeepsOthers(t *testing.T) {
	slug := "etagtriggerremoveone"
	sess, engine := setupTenantSchema(t, slug)
	adminConn, _ := openTestPool(t, 5*time.Second)

	modelDecls := []model.ModelDeclaration{etagWidgetModel(), etagGadgetModel()}
	auditedTables := []manifest.AuditedTable{{Table: "etag_widgets"}, {Table: "etag_gadgets"}}
	createAndSyncEtagWidget(t, sess, engine, modelDecls, auditedTables)

	schemaName := "tenant_" + slug
	if !etagTriggerExists(t, adminConn, schemaName, "etag_widgets") || !etagTriggerExists(t, adminConn, schemaName, "etag_gadgets") {
		t.Fatalf("expected both tables to have an etag trigger after install")
	}

	// etag_widgets removed from audited_tables; etag_gadgets stays declared.
	if err := engine.SyncEtagTriggers(context.Background(), sess, modelDecls, []manifest.AuditedTable{{Table: "etag_gadgets"}}); err != nil {
		t.Fatalf("SyncEtagTriggers() (remove one) error: %v", err)
	}

	if etagTriggerExists(t, adminConn, schemaName, "etag_widgets") {
		t.Errorf("etag_widgets still has its trigger after being removed from audited_tables")
	}
	if !etagTriggerExists(t, adminConn, schemaName, "etag_gadgets") {
		t.Errorf("etag_gadgets lost its trigger even though it's still declared")
	}
}

// A module uninstall (or a manifest edit removing every audited_tables[]
// entry) drops the etag trigger on every table this module owns, but
// never drops the shared update_etag() function itself — goerp#563 AC.
func TestSyncEtagTriggers_Reconciliation_EmptyAuditedTablesDropsAllTriggersKeepsFunction(t *testing.T) {
	slug := "etagtriggeremptyall"
	sess, engine := setupTenantSchema(t, slug)
	adminConn, _ := openTestPool(t, 5*time.Second)

	modelDecls := []model.ModelDeclaration{etagWidgetModel(), etagGadgetModel()}
	auditedTables := []manifest.AuditedTable{{Table: "etag_widgets"}, {Table: "etag_gadgets"}}
	createAndSyncEtagWidget(t, sess, engine, modelDecls, auditedTables)

	schemaName := "tenant_" + slug
	if !updateEtagFunctionExists(t, adminConn, schemaName) {
		t.Fatalf("expected update_etag() to exist after install")
	}

	// Simulate module uninstall (or every entry removed from
	// audited_tables) — modelDecls still reflects the module's
	// last-known owned tables, but no tables are declared audited.
	if err := engine.SyncEtagTriggers(context.Background(), sess, modelDecls, nil); err != nil {
		t.Fatalf("SyncEtagTriggers() (empty audited_tables) error: %v", err)
	}

	if etagTriggerExists(t, adminConn, schemaName, "etag_widgets") {
		t.Errorf("etag_widgets still has its trigger after audited_tables emptied")
	}
	if etagTriggerExists(t, adminConn, schemaName, "etag_gadgets") {
		t.Errorf("etag_gadgets still has its trigger after audited_tables emptied")
	}
	if !updateEtagFunctionExists(t, adminConn, schemaName) {
		t.Errorf("update_etag() was dropped — it's shared across every module's etag triggers, never owned by just one")
	}
}
