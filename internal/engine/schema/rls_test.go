package schema

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func ordersModel() model.ModelDeclaration {
	return *model.Define("sales.order", model.Table("sales_orders")).
		Field("id", model.UUID().Required()).
		Field("salesperson_id", model.UUID().Required()).
		Index("idx_sales_orders_id", model.BTreeIndex("id").Unique())
}

// openTestRLSReader creates (or reuses) a NOSUPERUSER, non-BYPASSRLS login
// role and returns a *sql.DB connected as it — RLS is a no-op for a
// superuser or BYPASSRLS role (multitenancy-internals.md §5a's
// "schema_sync_user bypass" section), and the dev-stack's default
// `goerp` role is a Postgres-image-created superuser, so a policy-filtering
// test needs a genuinely restricted role to mean anything.
func openTestRLSReader(t *testing.T, adminConn *sql.DB, schemaName, table string) *sql.DB {
	t.Helper()

	const roleName = "goerp_test_rls_reader"
	if _, err := adminConn.Exec(`
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + roleName + `') THEN
				CREATE ROLE ` + roleName + ` LOGIN PASSWORD 'dev' NOSUPERUSER NOBYPASSRLS;
			END IF;
		END
		$$;
	`); err != nil {
		t.Fatalf("create test reader role: %v", err)
	}
	if _, err := adminConn.Exec("GRANT USAGE ON SCHEMA " + quoteIdent(schemaName) + " TO " + roleName); err != nil {
		t.Fatalf("grant schema usage: %v", err)
	}
	if _, err := adminConn.Exec("GRANT SELECT ON " + table + " TO " + roleName); err != nil {
		t.Fatalf("grant select: %v", err)
	}

	readerConn, err := db.New("postgres://" + roleName + ":dev@localhost:55432/goerp")
	if err != nil {
		t.Fatalf("connect as test reader role: %v", err)
	}
	t.Cleanup(func() { _ = readerConn.Close() })

	return readerConn
}

func TestSyncRLSPolicies_OwnOnlyPolicy_FiltersRows(t *testing.T) {
	sess, engine := setupTenantSchema(t, "rlssynctest")
	adminConn, _ := openTestPool(t, 5*time.Second)

	modelDecls := []model.ModelDeclaration{ordersModel()}
	changes, err := engine.Diff(context.Background(), sess, modelDecls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, err := engine.Execute(context.Background(), sess, modelDecls, changes); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	policies := []manifest.Policy{{
		Name:      "sales:order:own_only",
		AppliesTo: "sales:order:read",
		Condition: "record.salesperson_id = current_user.contact_id OR user_has_role('sales_manager')",
	}}
	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, policies); err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v", err)
	}

	schemaName := "tenant_rlssynctest"
	table := quoteIdent(schemaName) + "." + quoteIdent("sales_orders")

	repID := "22222222-2222-2222-2222-222222222222"
	otherID := "33333333-3333-3333-3333-333333333333"
	insertRow(t, adminConn, table, repID)
	insertRow(t, adminConn, table, otherID)

	readerConn := openTestRLSReader(t, adminConn, schemaName, table)

	// A session scoped as the owning rep (repID) sees only its own row.
	if rows := countVisibleRows(t, readerConn, schemaName, table, repID, ""); rows != 1 {
		t.Fatalf("rep-scoped session saw %d rows, want 1", rows)
	}

	// A session scoped as an unrelated user sees zero rows.
	if rows := countVisibleRows(t, readerConn, schemaName, table, "55555555-5555-5555-5555-555555555555", ""); rows != 0 {
		t.Fatalf("unrelated session saw %d rows, want 0", rows)
	}

	// A session with the sales_manager role sees every row regardless of
	// salesperson_id.
	if rows := countVisibleRows(t, readerConn, schemaName, table, "44444444-4444-4444-4444-444444444444", "sales_manager"); rows != 2 {
		t.Fatalf("manager-scoped session saw %d rows, want 2", rows)
	}
}

func insertRow(t *testing.T, conn *sql.DB, table, salespersonID string) {
	t.Helper()
	// adminConn (the dev-stack's superuser `goerp` role) bypasses RLS
	// entirely, so this insert isn't itself subject to the policy under
	// test.
	if _, err := conn.Exec("INSERT INTO "+table+" (id, salesperson_id) VALUES (gen_random_uuid(), $1)", salespersonID); err != nil {
		t.Fatalf("insert row: %v", err)
	}
}

func countVisibleRows(t *testing.T, conn *sql.DB, schemaName, table, userContactID, role string) int {
	t.Helper()
	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("SET LOCAL search_path = " + quoteIdent(schemaName)); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	if _, err := tx.Exec("SELECT set_config('app.current_user_contact_id', $1, true)", userContactID); err != nil {
		t.Fatalf("set app.current_user_contact_id: %v", err)
	}
	if _, err := tx.Exec("SELECT set_config('app.current_user_roles', $1, true)", role); err != nil {
		t.Fatalf("set app.current_user_roles: %v", err)
	}

	var count int
	if err := tx.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}
