package schema

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/recordshares"
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

// sharedInvoiceModel has a bare (module-unqualified) Name so two
// differently-named modules' policies can both target it via the same
// resource token in applies_to — simulating a field_extension-style
// table two modules share.
func sharedInvoiceModel() model.ModelDeclaration {
	return *model.Define("invoice", model.Table("shared_invoices")).
		Field("id", model.UUID().Required()).
		Field("amount", model.Integer().Required())
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
	if len(names) != 1 || names[0] != "sales:order:managers_write" {
		t.Fatalf("after removing own_only, live policies = %v, want [sales:order:managers_write]", names)
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
	if names := livePolicyNames(t, adminConn, schemaName, "sales_invoices"); len(names) != 1 || names[0] != "sales:order:own_only" {
		t.Fatalf("after retarget, sales_invoices live policies = %v, want [sales:order:own_only]", names)
	}
}

// goerp#557: two modules whose names are prefix-related (both legal under
// manifest-spec.md §2's `^[a-z][a-z0-9_]{0,63}$`) declaring policies on
// the same shared table must never be confused for one another — the
// exact first-segment ownership match this fixes, replacing a substring
// prefix test that could have let "connector_paystack"'s reconciliation
// mistake "connector_paystack_v2"'s live policy for its own.
func TestSyncRLSPolicies_Reconciliation_DistinguishesPrefixRelatedModuleNames(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)
	tenantSlug := "rlsprefixtest"

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
	engine := NewSchemaDiffEngine(&Config{})
	modelDecls := []model.ModelDeclaration{sharedInvoiceModel()}

	shortSess, err := pool.BeginSync(context.Background(), tenantID, tenantSlug, "connector_paystack", testManifest("1.0.0"))
	if err != nil {
		t.Fatalf("BeginSync() (connector_paystack) error: %v", err)
	}
	t.Cleanup(func() { _ = shortSess.Close(context.Background()) })
	syncOrdersTable(t, engine, shortSess, modelDecls)

	shortModulePolicy := manifest.Policy{
		Name:      "connector_paystack:invoice:own_only",
		AppliesTo: "connector_paystack:invoice:read",
		Condition: "record.amount > 0",
	}
	if err := engine.SyncRLSPolicies(context.Background(), shortSess, modelDecls, []manifest.Policy{shortModulePolicy}); err != nil {
		t.Fatalf("SyncRLSPolicies() (connector_paystack install) error: %v", err)
	}

	longSess, err := pool.BeginSync(context.Background(), tenantID, tenantSlug, "connector_paystack_v2", testManifest("1.0.0"))
	if err != nil {
		t.Fatalf("BeginSync() (connector_paystack_v2) error: %v", err)
	}
	t.Cleanup(func() { _ = longSess.Close(context.Background()) })

	longModulePolicy := manifest.Policy{
		Name:      "connector_paystack_v2:invoice:v2_only",
		AppliesTo: "connector_paystack_v2:invoice:read",
		Condition: "record.amount > 100",
	}
	if err := engine.SyncRLSPolicies(context.Background(), longSess, modelDecls, []manifest.Policy{longModulePolicy}); err != nil {
		t.Fatalf("SyncRLSPolicies() (connector_paystack_v2 install) error: %v", err)
	}

	schemaName := "tenant_" + tenantSlug
	table := "shared_invoices"
	if names := livePolicyNames(t, conn, schemaName, table); len(names) != 2 {
		t.Fatalf("after both installs, live policies = %v, want 2", names)
	}

	// connector_paystack uninstalls (policies: nil) — its reconciliation
	// must never mistake connector_paystack_v2's policy for its own, even
	// though "connector_paystack_v2:..." starts with "connector_paystack".
	if err := engine.SyncRLSPolicies(context.Background(), shortSess, modelDecls, nil); err != nil {
		t.Fatalf("SyncRLSPolicies() (connector_paystack uninstall) error: %v", err)
	}

	names := livePolicyNames(t, conn, schemaName, table)
	if len(names) != 1 || names[0] != "connector_paystack_v2:invoice:v2_only" {
		t.Fatalf("after connector_paystack uninstall, live policies = %v, want [connector_paystack_v2:invoice:v2_only] only", names)
	}
	if !rlsEnabled(t, conn, schemaName, table) {
		t.Fatalf("RLS disabled even though connector_paystack_v2's policy still remains")
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

// shareableOrdersModel is ordersModel with two changes: an explicit
// .PrimaryKey() on id (syncShareWidening's primaryKeyColumnName needs a
// real IsPrimaryKey field, unlike ordersModel's own bare .Required()),
// and a bare, undotted Name ("order", not ordersModel's "sales.order") —
// route.RegisterModelRoutes's/registry.RegistrySnapshot.ModelByName's
// documented convention is moduleName + "." + the model's bare Name,
// which syncShareWidening's own qualifiedName also follows; ordersModel's
// dotted Name only happens to work for the ABAC-only tests above because
// resolvePolicyTarget separately accepts either form.
// .Shareable(perms...) is applied only when perms is non-nil — mirroring
// dispatch_meta_test.go's own "nil means don't declare Shareable at all"
// fixture convention, so a test can build a genuinely non-Shareable model
// via zero args.
func shareableOrdersModel(perms ...model.SharePermission) model.ModelDeclaration {
	opts := []model.ModelOption{model.Table("sales_orders")}
	if perms != nil {
		opts = append(opts, model.Shareable(perms...))
	}
	d := model.Define("order", opts...).
		Field("id", model.UUID().Required().PrimaryKey()).
		Field("salesperson_id", model.UUID().Required()).
		Index("idx_sales_orders_id", model.BTreeIndex("id").Unique())
	return *d
}

// bootstrapRecordShares creates record_shares in the tenant schema
// syncShareWidening's compiled EXISTS clause reads from — real deployments
// get this from tenant provisioning's CreateEngineTables activity, ahead
// of any module's own schema sync.
func bootstrapRecordShares(t *testing.T, conn *sql.DB, tenantSlug string) {
	t.Helper()
	if err := recordshares.NewStore(conn).Bootstrap(context.Background(), tenantSlug); err != nil {
		t.Fatalf("bootstrap record_shares: %v", err)
	}
}

// insertShare seeds a record_shares row directly (as the admin/superuser
// connection, bypassing RLS) — the row a widening policy's EXISTS clause
// is meant to match.
func insertShare(t *testing.T, conn *sql.DB, schemaName, qualifiedModel, recordID, sharedWithUserID, permission string) {
	t.Helper()
	if _, err := conn.Exec(
		"INSERT INTO "+quoteIdent(schemaName)+".record_shares (model, record_id, shared_with_user_id, permission, shared_by) VALUES ($1, $2, $3, $4, gen_random_uuid())",
		qualifiedModel, recordID, sharedWithUserID, permission,
	); err != nil {
		t.Fatalf("insert record_shares row: %v", err)
	}
}

// grantSelectOn grants the test reader role SELECT on an additional
// schema-qualified table — openTestRLSReader only grants the one table
// its own caller names, but a share-widening policy's EXISTS subquery
// against record_shares runs as the querying role, so the reader needs
// SELECT on record_shares too or the subquery itself fails on a
// permission error rather than evaluating to false.
func grantSelectOn(t *testing.T, adminConn *sql.DB, roleName, qualifiedTable string) {
	t.Helper()
	if _, err := adminConn.Exec("GRANT SELECT ON " + qualifiedTable + " TO " + roleName); err != nil {
		t.Fatalf("grant select on %s: %v", qualifiedTable, err)
	}
}

// countVisibleRowsAsUser is countVisibleRows's counterpart for
// syncShareWidening's own session variable — app.current_user_id, which
// its compiled EXISTS clause reads via the same current_setting(...)
// expression internal/engine/domain's UserAttr "id" case compiles ABAC
// current_user.id conditions to.
func countVisibleRowsAsUser(t *testing.T, conn *sql.DB, schemaName, table, userContactID, userID string) int {
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
	if _, err := tx.Exec("SELECT set_config('app.current_user_id', $1, true)", userID); err != nil {
		t.Fatalf("set app.current_user_id: %v", err)
	}

	var count int
	if err := tx.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func policyCmd(t *testing.T, conn *sql.DB, schemaName, table, policyName string) string {
	t.Helper()
	var cmd string
	if err := conn.QueryRow(
		"SELECT cmd FROM pg_policies WHERE schemaname = $1 AND tablename = $2 AND policyname = $3",
		schemaName, table, policyName,
	).Scan(&cmd); err != nil {
		t.Fatalf("query policy cmd for %q: %v", policyName, err)
	}
	return cmd
}

// A .Shareable(ReadShare) model, sharing an ABAC-restricted row with a
// user who isn't its owner and holds no ABAC-granting role, makes that
// row visible to the recipient — and only the recipient, not an
// unrelated third user with no share at all — goerp#471 AC.
func TestSyncShareWidening_ReadShareGrantsVisibilityToRecipientOnly(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "shareread", "sales")
	tenantSlug := "shareread"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare, model.WriteShare)}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v", err)
	}

	schemaName := "tenant_" + tenantSlug
	table := "sales_orders"
	qualifiedTable := quoteIdent(schemaName) + "." + quoteIdent(table)

	ownerID := "22222222-2222-2222-2222-222222222222"
	insertRow(t, adminConn, qualifiedTable, ownerID)

	var recordID string
	if err := adminConn.QueryRow("SELECT id FROM " + qualifiedTable + " LIMIT 1").Scan(&recordID); err != nil {
		t.Fatalf("read back inserted row id: %v", err)
	}

	recipientID := "66666666-6666-6666-6666-666666666666"
	insertShare(t, adminConn, schemaName, "sales.order", recordID, recipientID, "read")

	readerConn := openTestRLSReader(t, adminConn, schemaName, qualifiedTable)
	grantSelectOn(t, adminConn, "goerp_test_rls_reader", quoteIdent(schemaName)+".record_shares")

	// The share recipient — no ABAC-owning contact_id, no role — sees the
	// row purely via the share.
	if rows := countVisibleRowsAsUser(t, readerConn, schemaName, table, "77777777-7777-7777-7777-777777777777", recipientID); rows != 1 {
		t.Fatalf("recipient session saw %d rows, want 1", rows)
	}

	// An unrelated user with no share and no ABAC ownership sees nothing.
	unrelatedID := "88888888-8888-8888-8888-888888888888"
	if rows := countVisibleRowsAsUser(t, readerConn, schemaName, table, "99999999-9999-9999-9999-999999999999", unrelatedID); rows != 0 {
		t.Fatalf("unrelated session saw %d rows, want 0", rows)
	}
}

// A .Shareable() model with zero declared ABAC policies never gets RLS
// enabled, and gets no widening policy either — goerp#471 AC: no
// restrictive base policy means every permitted user already sees every
// row, so a share grant would be redundant.
func TestSyncShareWidening_ModelWithNoABACPolicies_NeverEnablesRLS(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharenoabac", "sales")
	tenantSlug := "sharenoabac"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare, model.WriteShare)}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, nil); err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v", err)
	}

	schemaName := "tenant_" + tenantSlug
	table := "sales_orders"
	if names := livePolicyNames(t, adminConn, schemaName, table); len(names) != 0 {
		t.Fatalf("live policies = %v, want none — no ABAC policy was ever declared", names)
	}
	if rlsEnabled(t, adminConn, schemaName, table) {
		t.Fatalf("RLS enabled on a .Shareable() table with zero declared ABAC policies")
	}
}

// A write-share widening policy is installed FOR ALL, and a read-share
// widening policy FOR SELECT only — never the reverse — goerp#471 AC /
// go-sdk-reference.md §22: "a write share only widens the FOR ALL
// policy, never FOR SELECT alone."
func TestSyncShareWidening_WriteSharePolicyIsForAll_ReadSharePolicyIsForSelect(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharecmd", "sales")
	tenantSlug := "sharecmd"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare, model.WriteShare)}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy(), managersWritePolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v", err)
	}

	schemaName := "tenant_" + tenantSlug
	table := "sales_orders"

	if cmd := policyCmd(t, adminConn, schemaName, table, "sales:order:__share_read"); cmd != "SELECT" {
		t.Errorf("read-share policy cmd = %q, want SELECT", cmd)
	}
	if cmd := policyCmd(t, adminConn, schemaName, table, "sales:order:__share_write"); cmd != "ALL" {
		t.Errorf("write-share policy cmd = %q, want ALL", cmd)
	}
}

// Dropping WriteShare from a model's declared SharePerms on a later sync
// drops only the write-share widening policy, leaving the read-share one
// (and the underlying ABAC policy) untouched — the same reconciliation
// guarantee TestSyncRLSPolicies_RemovingOnePolicyKeepsOthersOnSameTable
// already exercises for ordinary ABAC policies, now for widening
// policies going through the same desired-map mechanism.
func TestSyncShareWidening_RemovingSharePermDropsOnlyThatWideningPolicy(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharedrop", "sales")
	tenantSlug := "sharedrop"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	schemaName := "tenant_" + tenantSlug
	table := "sales_orders"

	bothShared := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare, model.WriteShare)}
	syncOrdersTable(t, engine, sess, bothShared)
	if err := engine.SyncRLSPolicies(context.Background(), sess, bothShared, []manifest.Policy{ownOnlyPolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (both perms) error: %v", err)
	}
	if names := livePolicyNames(t, adminConn, schemaName, table); len(names) != 3 {
		t.Fatalf("after install, live policies = %v, want 3 (ABAC + read-share + write-share)", names)
	}

	readOnlyShared := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare)}
	if err := engine.SyncRLSPolicies(context.Background(), sess, readOnlyShared, []manifest.Policy{ownOnlyPolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (read-only perm) error: %v", err)
	}

	names := livePolicyNames(t, adminConn, schemaName, table)
	if len(names) != 2 {
		t.Fatalf("after dropping WriteShare, live policies = %v, want 2 (ABAC + read-share)", names)
	}
	for _, n := range names {
		if n == "sales:order:__share_write" {
			t.Fatalf("write-share policy %q still present after WriteShare removed from SharePerms", n)
		}
	}
}

// An unrecognized SharePermission value (SharePermission is a bare string
// type, so a typo'd or bypassed-constant value compiles fine) errors
// instead of silently skipping — a model that looks Shareable must not
// silently get zero widening for a permission no one notices was never
// installed.
func TestSyncShareWidening_UnrecognizedSharePermissionErrors(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharebadperm", "sales")
	tenantSlug := "sharebadperm"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.SharePermission("delete"))}
	syncOrdersTable(t, engine, sess, modelDecls)

	err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()})
	if err == nil {
		t.Fatal("SyncRLSPolicies() error = nil, want an error for an unrecognized SharePermission")
	}
}

// A .Shareable() model whose primary key isn't UUID-kind errors at sync
// time with a clear message, instead of surfacing as an opaque Postgres
// "operator does not exist: <type> = uuid" the first time the compiled
// policy is evaluated. record_shares.record_id is UUID, and nothing in
// the SDK stops a module from declaring a non-UUID primary key.
func TestSyncShareWidening_NonUUIDPrimaryKeyErrors(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharebadpk", "sales")
	tenantSlug := "sharebadpk"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	badPKModel := *model.Define("order", model.Table("sales_orders"), model.Shareable(model.ReadShare)).
		Field("id", model.Integer().Required().PrimaryKey()).
		Field("salesperson_id", model.UUID().Required())
	modelDecls := []model.ModelDeclaration{badPKModel}
	syncOrdersTable(t, engine, sess, modelDecls)

	err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{{
		Name:      "sales:order:own_only",
		AppliesTo: "sales:order:read",
		Condition: "record.salesperson_id = current_user.contact_id",
	}})
	if err == nil {
		t.Fatal("SyncRLSPolicies() error = nil, want an error for a non-UUID primary key")
	}
}

// A session with app.current_user_id set to an empty string (rather than
// left unset) — e.g. a workflow activity dispatched with no live user,
// modCtx.UserID == "" (internal/engine/wasm/tenant_scope.go always calls
// set_config with whatever UserID it's given) — must not error out of a
// query against a .Shareable() table entirely; it should simply see no
// share-widened rows, the same way it already sees no ABAC-widened rows
// for a policy it fails — goerp#471 /code-review: a bare ”::uuid cast
// errors, unlike NULLIF(...,”)::uuid.
func TestSyncShareWidening_EmptyCurrentUserIDDoesNotError(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "shareemptyuser", "sales")
	tenantSlug := "shareemptyuser"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare)}
	syncOrdersTable(t, engine, sess, modelDecls)
	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v", err)
	}

	schemaName := "tenant_" + tenantSlug
	table := "sales_orders"
	qualifiedTable := quoteIdent(schemaName) + "." + quoteIdent(table)
	insertRow(t, adminConn, qualifiedTable, "22222222-2222-2222-2222-222222222222")

	readerConn := openTestRLSReader(t, adminConn, schemaName, qualifiedTable)
	grantSelectOn(t, adminConn, "goerp_test_rls_reader", quoteIdent(schemaName)+".record_shares")

	rows := countVisibleRowsAsUser(t, readerConn, schemaName, table, "99999999-9999-9999-9999-999999999999", "")
	if rows != 0 {
		t.Fatalf("session with empty app.current_user_id saw %d rows, want 0 (and no query error)", rows)
	}
}

// A .Shareable() model with a composite primary key (more than one
// .PrimaryKey() field, which atlas.go's toAtlasTable otherwise supports)
// errors at sync time instead of silently keying the widening policy off
// just the first PK column found — record_shares.record_id is a single
// UUID column, so it can't represent a composite key at all, and keying
// off only part of it would widen access too broadly — goerp#471
// /code-review.
func TestSyncShareWidening_CompositePrimaryKeyErrors(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharecompositepk", "sales")
	tenantSlug := "sharecompositepk"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	compositePKModel := *model.Define("order", model.Table("sales_orders"), model.Shareable(model.ReadShare)).
		Field("id", model.UUID().Required().PrimaryKey()).
		Field("salesperson_id", model.UUID().Required().PrimaryKey())
	modelDecls := []model.ModelDeclaration{compositePKModel}
	syncOrdersTable(t, engine, sess, modelDecls)

	err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{{
		Name:      "sales:order:own_only",
		AppliesTo: "sales:order:read",
		Condition: "record.salesperson_id = current_user.contact_id",
	}})
	if err == nil {
		t.Fatal("SyncRLSPolicies() error = nil, want an error for a composite primary key")
	}
}

// A tenant whose schema predates .Shareable() (or was provisioned via a
// path that skipped record_shares.Bootstrap — regular module sync never
// revisits it, only tenant provisioning's own CreateEngineTables activity
// does) still syncs successfully: syncShareWidening creates record_shares
// itself instead of hard-failing with "relation record_shares does not
// exist" the moment a .Shareable() model tries to install a policy
// against it.
func TestSyncShareWidening_CreatesRecordSharesTableIfMissing(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharenoprovision", "sales")
	tenantSlug := "sharenoprovision"
	// Deliberately no bootstrapRecordShares(t, adminConn, tenantSlug) call
	// here — this is the whole point of the test.

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare)}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v", err)
	}

	schemaName := "tenant_" + tenantSlug
	var tableExists bool
	if err := adminConn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'record_shares')",
		schemaName,
	).Scan(&tableExists); err != nil {
		t.Fatalf("check record_shares table: %v", err)
	}
	if !tableExists {
		t.Fatal("expected record_shares to have been created by syncShareWidening")
	}

	if names := livePolicyNames(t, adminConn, schemaName, "sales_orders"); len(names) != 2 {
		t.Fatalf("live policies = %v, want 2 (ABAC + read-share)", names)
	}
}

// model.Shareable() called with zero SharePermission args is legal (a
// model opted into being shareable ahead of picking which permission
// levels to actually offer) and installs zero widening policies — the
// PK-shape validation must not run for it, since no widening SQL
// referencing the PK is ever built for a model with nothing in
// SharePerms. A non-UUID PK here must not fail the sync.
func TestSyncShareWidening_ZeroSharePermsSkipsPKValidation(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharezeroperms", "sales")
	tenantSlug := "sharezeroperms"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	// Shareable() with zero args and a non-UUID PK — would error if the PK
	// check ran unconditionally.
	shareableNoPermsModel := *model.Define("order", model.Table("sales_orders"), model.Shareable()).
		Field("id", model.Integer().Required().PrimaryKey()).
		Field("salesperson_id", model.UUID().Required())
	modelDecls := []model.ModelDeclaration{shareableNoPermsModel}
	syncOrdersTable(t, engine, sess, modelDecls)

	err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{{
		Name:      "sales:order:own_only",
		AppliesTo: "sales:order:read",
		Condition: "record.salesperson_id = current_user.contact_id",
	}})
	if err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v, want nil — zero SharePerms should skip PK validation", err)
	}

	schemaName := "tenant_" + tenantSlug
	if names := livePolicyNames(t, adminConn, schemaName, "sales_orders"); len(names) != 1 {
		t.Fatalf("live policies = %v, want 1 (ABAC only, no widening policy)", names)
	}
}

// TestSyncShareWidening_ConcurrentFirstUseAcrossModulesAllSucceed guards
// against goerp#171 directly, for ensureRecordSharesTable specifically —
// BeginSync's own advisory lock is scoped to (tenant, module), not tenant
// alone, so two different modules' syncs against the same tenant, both
// hitting a still-missing record_shares table for the first time, run
// concurrently. Without ensureRecordSharesTable's own tenant-scoped
// pg_advisory_xact_lock (the same key recordshares.Store.Bootstrap
// takes), a bare "CREATE TABLE IF NOT EXISTS" from both at once can race.
func TestSyncShareWidening_ConcurrentFirstUseAcrossModulesAllSucceed(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)
	tenantSlug := "shareconcurrent"

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
	engine := NewSchemaDiffEngine(&Config{})

	const moduleCount = 5
	var wg sync.WaitGroup
	errs := make(chan error, moduleCount)
	for i := range moduleCount {
		moduleName := fmt.Sprintf("shareconcurrentmod%d", i)
		wg.Go(func() {
			sess, err := pool.BeginSync(context.Background(), tenantID, tenantSlug, moduleName, testManifest("1.0.0"))
			if err != nil {
				errs <- fmt.Errorf("%s: BeginSync: %w", moduleName, err)
				return
			}
			defer func() { _ = sess.Close(context.Background()) }()

			tableName := fmt.Sprintf("orders_%d", i)
			modelDecls := []model.ModelDeclaration{
				*model.Define("order", model.Table(tableName), model.Shareable(model.ReadShare)).
					Field("id", model.UUID().Required().PrimaryKey()).
					Field("salesperson_id", model.UUID().Required()),
			}
			changes, err := engine.Diff(context.Background(), sess, modelDecls, nil)
			if err != nil {
				errs <- fmt.Errorf("%s: Diff: %w", moduleName, err)
				return
			}
			if _, _, err := engine.ExecuteAccepted(context.Background(), sess, modelDecls, changes, nil); err != nil {
				errs <- fmt.Errorf("%s: Execute: %w", moduleName, err)
				return
			}

			policy := manifest.Policy{
				Name:      moduleName + ":order:own_only",
				AppliesTo: moduleName + ":order:read",
				Condition: "record.salesperson_id = current_user.contact_id",
			}
			if err := engine.SyncRLSPolicies(context.Background(), sess, modelDecls, []manifest.Policy{policy}); err != nil {
				errs <- fmt.Errorf("%s: SyncRLSPolicies: %w", moduleName, err)
				return
			}
			errs <- nil
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent module sync error: %v", err)
		}
	}
}
