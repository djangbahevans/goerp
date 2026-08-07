package schema

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// setupTenantSchema drops and recreates a bare tenant schema, then opens a
// SchemaSyncSession against it the same way real schema sync would — via
// SchemaSyncPool.BeginSync, so these tests exercise the real advisory-lock
// and search_path setup, not a shortcut connection.
func setupTenantSchema(t *testing.T, tenantSlug string) (*SchemaSyncSession, *SchemaDiffEngine) {
	t.Helper()

	conn, pool := openTestPool(t, 5*time.Second)

	if _, err := conn.Exec("DROP SCHEMA IF EXISTS " + quoteIdent("tenant_"+tenantSlug) + " CASCADE"); err != nil {
		t.Fatalf("drop tenant schema: %v", err)
	}
	if _, err := conn.Exec("CREATE SCHEMA " + quoteIdent("tenant_"+tenantSlug)); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec("DROP SCHEMA IF EXISTS " + quoteIdent("tenant_"+tenantSlug) + " CASCADE")
	})

	tenantID := "44444444-4444-4444-4444-444444444444"
	sess, err := pool.BeginSync(context.Background(), tenantID, tenantSlug, "testmodule", testManifest("1.0.0"))
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close(context.Background()) })

	engine := NewSchemaDiffEngine(&Config{})
	return sess, engine
}

func tableExists(t *testing.T, conn *sql.DB, schemaName, table string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)",
		schemaName, table,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("tableExists query: %v", err)
	}
	return exists
}

func columnExists(t *testing.T, conn *sql.DB, schemaName, table, column string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 AND column_name = $3)",
		schemaName, table, column,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("columnExists query: %v", err)
	}
	return exists
}

func indexExists(t *testing.T, conn *sql.DB, schemaName, index string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND indexname = $2)",
		schemaName, index,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("indexExists query: %v", err)
	}
	return exists
}

func widgetModel() model.ModelDeclaration {
	return *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("sku", model.Char(40).Required()).
		Index("idx_widgets_sku", model.BTreeIndex("sku").Unique())
}

func TestDiffAndExecute_CreatesNewTableSafely(t *testing.T) {
	sess, engine := setupTenantSchema(t, "difftest_newtable")
	conn, _ := openTestPool(t, 5*time.Second)

	modelDecls := []model.ModelDeclaration{widgetModel()}

	changes, err := engine.Diff(context.Background(), sess, modelDecls)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("Diff() on an empty schema returned no changes, want at least AddTable")
	}

	blocked, err := engine.Execute(context.Background(), sess, modelDecls, changes)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(blocked) != 0 {
		t.Errorf("Execute() blocked = %v, want none for a brand-new table", blocked)
	}

	if !tableExists(t, conn, "tenant_difftest_newtable", "widgets") {
		t.Error("widgets table was not created")
	}
	if !columnExists(t, conn, "tenant_difftest_newtable", "widgets", "sku") {
		t.Error("sku column was not created")
	}
	if !indexExists(t, conn, "tenant_difftest_newtable", "idx_widgets_sku") {
		t.Error("idx_widgets_sku index was not created")
	}

	// Re-running Diff against the now-synced schema should find nothing left
	// to do — proves the round trip (declared -> Atlas -> live) is stable.
	changes, err = engine.Diff(context.Background(), sess, modelDecls)
	if err != nil {
		t.Fatalf("second Diff() error: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("second Diff() on an already-synced schema = %v, want no changes", changes)
	}
}

func TestExecute_SafeAddColumnApplied(t *testing.T) {
	sess, engine := setupTenantSchema(t, "difftest_addcol")
	conn, _ := openTestPool(t, 5*time.Second)

	base := []model.ModelDeclaration{widgetModel()}
	changes, err := engine.Diff(context.Background(), sess, base)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, err := engine.Execute(context.Background(), sess, base, changes); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	withExtra := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("sku", model.Char(40).Required()).
		Field("notes", model.Text()). // nullable — safe to add automatically
		Index("idx_widgets_sku", model.BTreeIndex("sku").Unique())
	modelDecls := []model.ModelDeclaration{withExtra}

	changes, err = engine.Diff(context.Background(), sess, modelDecls)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	blocked, err := engine.Execute(context.Background(), sess, modelDecls, changes)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(blocked) != 0 {
		t.Errorf("Execute() blocked = %v, want none for a nullable AddColumn", blocked)
	}
	if !columnExists(t, conn, "tenant_difftest_addcol", "widgets", "notes") {
		t.Error("notes column was not added")
	}
}

func TestExecute_UnsafeDropColumnBlocked(t *testing.T) {
	sess, engine := setupTenantSchema(t, "difftest_dropcol")
	conn, _ := openTestPool(t, 5*time.Second)

	base := []model.ModelDeclaration{widgetModel()}
	changes, err := engine.Diff(context.Background(), sess, base)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, err := engine.Execute(context.Background(), sess, base, changes); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Declares the model without "sku" — a DropColumn change.
	narrowed := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required())
	modelDecls := []model.ModelDeclaration{narrowed}

	changes, err = engine.Diff(context.Background(), sess, modelDecls)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	blocked, err := engine.Execute(context.Background(), sess, modelDecls, changes)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(blocked) != 1 {
		t.Fatalf("Execute() blocked = %v, want exactly one DropColumn", blocked)
	}

	// The column must still be there — a blocked change is never applied.
	if !columnExists(t, conn, "tenant_difftest_dropcol", "widgets", "sku") {
		t.Error("sku column was dropped even though the change was blocked")
	}
}

func TestApply_AddIndexUsesConcurrently(t *testing.T) {
	sess, engine := setupTenantSchema(t, "difftest_concurrently")
	conn, _ := openTestPool(t, 5*time.Second)

	// A table with no index yet.
	base := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required())
	changes, err := engine.Diff(context.Background(), sess, []model.ModelDeclaration{base})
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, err := engine.Execute(context.Background(), sess, []model.ModelDeclaration{base}, changes); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	withIndex := *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Index("idx_widgets_name", model.BTreeIndex("name"))
	modelDecls := []model.ModelDeclaration{withIndex}

	changes, err = engine.Diff(context.Background(), sess, modelDecls)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}

	// Build the exact DDL Execute will run for this change and assert it
	// literally contains CONCURRENTLY — the one requirement #19's
	// acceptance criteria calls out by name. This is the direct,
	// unambiguous check: CONCURRENTLY statements can't run inside a
	// transaction block at all, so there's no "run it inside an explicit tx
	// and see if it fails" alternative to assert against here.
	nonTx, _ := splitNonTransactional(explodeChanges(changes))
	if len(nonTx) != 1 {
		t.Fatalf("splitNonTransactional() non-transactional changes = %d, want 1 (the new index)", len(nonTx))
	}
	cmd, err := concurrentIndexDDL(modelDecls, nonTx[0].change)
	if err != nil {
		t.Fatalf("concurrentIndexDDL() error: %v", err)
	}
	if !strings.Contains(cmd, "CONCURRENTLY") {
		t.Errorf("concurrentIndexDDL() = %q, want it to contain CONCURRENTLY", cmd)
	}

	blocked, err := engine.Execute(context.Background(), sess, modelDecls, changes)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(blocked) != 0 {
		t.Errorf("Execute() blocked = %v, want none", blocked)
	}
	if !indexExists(t, conn, "tenant_difftest_concurrently", "idx_widgets_name") {
		t.Error("idx_widgets_name was not created")
	}
}
