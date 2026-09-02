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

func invoicesModel() model.ModelDeclaration {
	return *model.Define("sales.invoice", model.Table("sales_invoices")).
		Field("id", model.UUID().Required()).
		Field("salesperson_id", model.UUID().Required()).
		Index("idx_sales_invoices_id", model.BTreeIndex("id").Unique())
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
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, modelDecls, changes, nil); err != nil {
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

// setupTenantSchemaForModule is setupTenantSchema (diff_test.go) with the
// syncing module's name parameterized instead of always "testmodule" —
// the reconciliation tests below need it to match the module segment of
// the policy names they install, and it also hands back the admin *sql.DB
// so a test can inspect pg_policies/pg_class directly.
func setupTenantSchemaForModule(t *testing.T, tenantSlug, moduleName string) (*SchemaSyncSession, *SchemaDiffEngine, *sql.DB) {
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
	sess, err := pool.BeginSync(context.Background(), tenantID, tenantSlug, moduleName, testManifest("1.0.0"))
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close(context.Background()) })

	return sess, NewSchemaDiffEngine(&Config{}), conn
}

func livePolicyNames(t *testing.T, conn *sql.DB, schemaName, table string) []string {
	t.Helper()
	names, err := listRLSPolicyNames(context.Background(), conn, schemaName, table)
	if err != nil {
		t.Fatalf("listRLSPolicyNames: %v", err)
	}
	return names
}

func rlsEnabled(t *testing.T, conn *sql.DB, schemaName, table string) bool {
	t.Helper()
	var enabled bool
	err := conn.QueryRow(`
		SELECT relrowsecurity FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2
	`, schemaName, table).Scan(&enabled)
	if err != nil {
		t.Fatalf("query relrowsecurity: %v", err)
	}
	return enabled
}

func ownOnlyPolicy() manifest.Policy {
	return manifest.Policy{
		Name:      "sales:order:own_only",
		AppliesTo: "sales:order:read",
		Condition: "record.salesperson_id = current_user.contact_id OR user_has_role('sales_manager')",
	}
}

func managersWritePolicy() manifest.Policy {
	return manifest.Policy{
		Name:      "sales:order:managers_write",
		AppliesTo: "sales:order:write",
		Condition: "user_has_role('sales_manager')",
	}
}

func syncOrdersTable(t *testing.T, engine *SchemaDiffEngine, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration) {
	t.Helper()
	changes, err := engine.Diff(context.Background(), sess, modelDecls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, modelDecls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
}

// Removing a policy from the manifest (module stays installed) drops
// exactly that policy on the next sync, leaving any other policy on the
// same table untouched — goerp#470 AC.
func TestSyncRLSPolicies_RemovingOnePolicyKeepsOthersOnSameTable(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "rlsremovetest", "sales")
	modelDecls := []model.ModelDeclaration{ordersModel()}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy(), managersWritePolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (install both) error: %v", err)
	}

	schemaName := "tenant_rlsremovetest"
	table := "sales_orders"
	if names := livePolicyNames(t, adminConn, schemaName, table); len(names) != 2 {
		t.Fatalf("after install, live policies = %v, want 2", names)
	}

	// own_only removed from the manifest; managers_write stays declared.
	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{managersWritePolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (remove one) error: %v", err)
	}

	names := livePolicyNames(t, adminConn, schemaName, table)
	if len(names) != 1 || names[0] != "sales_order_managers_write" {
		t.Fatalf("after removing own_only, live policies = %v, want [sales_order_managers_write]", names)
	}
	if !rlsEnabled(t, adminConn, schemaName, table) {
		t.Fatalf("RLS disabled even though managers_write policy still remains")
	}
}

// Module uninstall calls SyncRLSPolicies with the module's own last-known
// modelDecls but policies: nil — goerp#470 AC: every policy the module
// owned drops, and RLS is disabled on the table left with none.
func TestSyncRLSPolicies_ModuleUninstall_DropsAllAndDisablesRLS(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "rlsuninstalltest", "sales")
	modelDecls := []model.ModelDeclaration{ordersModel()}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy(), managersWritePolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (install) error: %v", err)
	}

	schemaName := "tenant_rlsuninstalltest"
	table := "sales_orders"

	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, nil); err != nil {
		t.Fatalf("SyncRLSPolicies() (uninstall) error: %v", err)
	}

	if names := livePolicyNames(t, adminConn, schemaName, table); len(names) != 0 {
		t.Fatalf("after uninstall, live policies = %v, want none", names)
	}
	if rlsEnabled(t, adminConn, schemaName, table) {
		t.Fatalf("RLS still enabled after module uninstall dropped every policy")
	}
}

// A table's RLS policies are matched only by the module's own
// name-transform convention — reconciliation never drops a policy it
// can't attribute to itself, and never disables RLS while a policy it
// doesn't own is still standing (the field_extension-style shared-table
// case from multitenancy-internals.md §5a) — goerp#470 AC.
func TestSyncRLSPolicies_Reconciliation_NeverTouchesForeignModulePolicy(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "rlsforeigntest", "sales")
	modelDecls := []model.ModelDeclaration{ordersModel()}
	syncOrdersTable(t, engine, sess, modelDecls)

	schemaName := "tenant_rlsforeigntest"
	table := "sales_orders"
	qualifiedTable := quoteIdent(schemaName) + "." + quoteIdent(table)

	const foreignPolicy = "otherapp_shared_view"
	if _, err := adminConn.Exec("ALTER TABLE " + qualifiedTable + " ENABLE ROW LEVEL SECURITY"); err != nil {
		t.Fatalf("enable RLS: %v", err)
	}
	if _, err := adminConn.Exec("CREATE POLICY " + quoteIdent(foreignPolicy) + " ON " + qualifiedTable + " FOR SELECT USING (true)"); err != nil {
		t.Fatalf("create foreign policy: %v", err)
	}

	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (install) error: %v", err)
	}
	if names := livePolicyNames(t, adminConn, schemaName, table); len(names) != 2 {
		t.Fatalf("after install, live policies = %v, want this module's + foreign", names)
	}

	// Simulate module uninstall for "sales" — an empty desired set.
	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, nil); err != nil {
		t.Fatalf("SyncRLSPolicies() (uninstall) error: %v", err)
	}

	names := livePolicyNames(t, adminConn, schemaName, table)
	if len(names) != 1 || names[0] != foreignPolicy {
		t.Fatalf("after uninstall, live policies = %v, want [%s] only", names, foreignPolicy)
	}
	if !rlsEnabled(t, adminConn, schemaName, table) {
		t.Fatalf("RLS disabled even though a foreign policy still remains on the table")
	}
}

// A policy's manifest `name` isn't required to keep pointing at the same
// applies_to resource across a sync (validatePolicies only checks
// applies_to against declared permissions, never against name's own
// resource segment) — reconciliation must drop the stale policy left on
// the resource's old table, not treat it as still desired just because a
// same-named policy is desired somewhere else on this sync.
func TestSyncRLSPolicies_Reconciliation_DropsStalePolicyWhenNameRetargetsTable(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "rlsretargettest", "sales")
	modelDecls := []model.ModelDeclaration{ordersModel(), invoicesModel()}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (install on orders) error: %v", err)
	}

	schemaName := "tenant_rlsretargettest"
	if names := livePolicyNames(t, adminConn, schemaName, "sales_orders"); len(names) != 1 {
		t.Fatalf("after install, sales_orders live policies = %v, want 1", names)
	}

	// Same policy name, applies_to edited to a different resource — as if
	// the manifest moved this ABAC rule from orders to invoices across a
	// version bump.
	retargeted := manifest.Policy{
		Name:      "sales:order:own_only",
		AppliesTo: "sales:invoice:read",
		Condition: "record.salesperson_id = current_user.contact_id",
	}
	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{retargeted}); err != nil {
		t.Fatalf("SyncRLSPolicies() (retarget) error: %v", err)
	}

	if names := livePolicyNames(t, adminConn, schemaName, "sales_orders"); len(names) != 0 {
		t.Fatalf("after retarget, stale policies left on sales_orders = %v, want none", names)
	}
	if rlsEnabled(t, adminConn, schemaName, "sales_orders") {
		t.Fatalf("RLS still enabled on sales_orders after its only policy was retargeted away")
	}
	if names := livePolicyNames(t, adminConn, schemaName, "sales_invoices"); len(names) != 1 || names[0] != "sales_order_own_only" {
		t.Fatalf("after retarget, sales_invoices live policies = %v, want [sales_order_own_only]", names)
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
