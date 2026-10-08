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

	readerConn, err := db.New("postgres://" + roleName + ":dev@localhost:15432/goerp")
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
	changes, err := engine.Diff(t.Context(), sess, modelDecls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(t.Context(), sess, modelDecls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	policies := []manifest.Policy{{
		Name:      "sales:order:own_only",
		AppliesTo: "sales:order:read",
		Condition: "record.salesperson_id = current_user.contact_id OR user_has_role('sales_manager')",
	}}
	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, policies); err != nil {
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

// Reconciliation needs a matching module prefix and an admin connection to inspect
// PostgreSQL policy catalogs.
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
	sess, err := pool.BeginSync(t.Context(), tenantID, tenantSlug, moduleName, testManifest("1.0.0"))
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close(context.Background()) })

	return sess, NewSchemaDiffEngine(&Config{}), conn
}

func livePolicyNames(t *testing.T, conn *sql.DB, schemaName, table string) []string {
	t.Helper()
	names, err := listRLSPolicyNames(t.Context(), conn, schemaName, table)
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
	changes, err := engine.Diff(t.Context(), sess, modelDecls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(t.Context(), sess, modelDecls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
}

func TestSyncRLSPolicies_RemovingOnePolicyKeepsOthersOnSameTable(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "rlsremovetest", "sales")
	modelDecls := []model.ModelDeclaration{ordersModel()}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy(), managersWritePolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (install both) error: %v", err)
	}

	schemaName := "tenant_rlsremovetest"
	table := "sales_orders"
	if names := livePolicyNames(t, adminConn, schemaName, table); len(names) != 2 {
		t.Fatalf("after install, live policies = %v, want 2", names)
	}

	// own_only removed from the manifest; managers_write stays declared.
	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{managersWritePolicy()}); err != nil {
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

func TestSyncRLSPolicies_ModuleUninstall_DropsAllAndDisablesRLS(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "rlsuninstalltest", "sales")
	modelDecls := []model.ModelDeclaration{ordersModel()}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy(), managersWritePolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (install) error: %v", err)
	}

	schemaName := "tenant_rlsuninstalltest"
	table := "sales_orders"

	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, nil); err != nil {
		t.Fatalf("SyncRLSPolicies() (uninstall) error: %v", err)
	}

	if names := livePolicyNames(t, adminConn, schemaName, table); len(names) != 0 {
		t.Fatalf("after uninstall, live policies = %v, want none", names)
	}
	if rlsEnabled(t, adminConn, schemaName, table) {
		t.Fatalf("RLS still enabled after module uninstall dropped every policy")
	}
}

// Reconciliation must preserve foreign policies and keep RLS enabled while any policy
// remains.
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

	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (install) error: %v", err)
	}
	if names := livePolicyNames(t, adminConn, schemaName, table); len(names) != 2 {
		t.Fatalf("after install, live policies = %v, want this module's + foreign", names)
	}

	// Simulate module uninstall for "sales" — an empty desired set.
	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, nil); err != nil {
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

// A policy name can move to another table; reconciliation must remove the old table's
// policy even when the name remains desired elsewhere.
func TestSyncRLSPolicies_Reconciliation_DropsStalePolicyWhenNameRetargetsTable(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "rlsretargettest", "sales")
	modelDecls := []model.ModelDeclaration{ordersModel(), invoicesModel()}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
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
	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{retargeted}); err != nil {
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

	shortSess, err := pool.BeginSync(t.Context(), tenantID, tenantSlug, "connector_paystack", testManifest("1.0.0"))
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
	if err := engine.SyncRLSPolicies(t.Context(), shortSess, modelDecls, []manifest.Policy{shortModulePolicy}); err != nil {
		t.Fatalf("SyncRLSPolicies() (connector_paystack install) error: %v", err)
	}

	longSess, err := pool.BeginSync(t.Context(), tenantID, tenantSlug, "connector_paystack_v2", testManifest("1.0.0"))
	if err != nil {
		t.Fatalf("BeginSync() (connector_paystack_v2) error: %v", err)
	}
	t.Cleanup(func() { _ = longSess.Close(context.Background()) })

	longModulePolicy := manifest.Policy{
		Name:      "connector_paystack_v2:invoice:v2_only",
		AppliesTo: "connector_paystack_v2:invoice:read",
		Condition: "record.amount > 100",
	}
	if err := engine.SyncRLSPolicies(t.Context(), longSess, modelDecls, []manifest.Policy{longModulePolicy}); err != nil {
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
	if err := engine.SyncRLSPolicies(t.Context(), shortSess, modelDecls, nil); err != nil {
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

// Use a bare model name and explicit primary key for share policy resolution. Nil perms
// leaves the model without a Shareable declaration.
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
	if err := recordshares.NewStore(conn).Bootstrap(t.Context(), tenantSlug); err != nil {
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

func TestSyncShareWidening_ReadShareGrantsVisibilityToRecipientOnly(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "shareread", "sales")
	tenantSlug := "shareread"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare, model.WriteShare)}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
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

// Without restrictive ABAC policies, sharing does not need RLS widening because permitted
// users already see all rows.
func TestSyncShareWidening_ModelWithNoABACPolicies_NeverEnablesRLS(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharenoabac", "sales")
	tenantSlug := "sharenoabac"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare, model.WriteShare)}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, nil); err != nil {
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

func TestSyncShareWidening_WriteSharePolicyIsForAll_ReadSharePolicyIsForSelect(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharecmd", "sales")
	tenantSlug := "sharecmd"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare, model.WriteShare)}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy(), managersWritePolicy()}); err != nil {
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

func TestSyncShareWidening_RemovingSharePermDropsOnlyThatWideningPolicy(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharedrop", "sales")
	tenantSlug := "sharedrop"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	schemaName := "tenant_" + tenantSlug
	table := "sales_orders"

	bothShared := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare, model.WriteShare)}
	syncOrdersTable(t, engine, sess, bothShared)
	if err := engine.SyncRLSPolicies(t.Context(), sess, bothShared, []manifest.Policy{ownOnlyPolicy()}); err != nil {
		t.Fatalf("SyncRLSPolicies() (both perms) error: %v", err)
	}
	if names := livePolicyNames(t, adminConn, schemaName, table); len(names) != 3 {
		t.Fatalf("after install, live policies = %v, want 3 (ABAC + read-share + write-share)", names)
	}

	readOnlyShared := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare)}
	if err := engine.SyncRLSPolicies(t.Context(), sess, readOnlyShared, []manifest.Policy{ownOnlyPolicy()}); err != nil {
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

func TestSyncShareWidening_UnrecognizedSharePermissionErrors(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharebadperm", "sales")
	tenantSlug := "sharebadperm"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.SharePermission("delete"))}
	syncOrdersTable(t, engine, sess, modelDecls)

	err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()})
	if err == nil {
		t.Fatal("SyncRLSPolicies() error = nil, want an error for an unrecognized SharePermission")
	}
}

// Shares store UUID record IDs, so non-UUID primary keys must fail schema validation
// before producing unusable RLS.
func TestSyncShareWidening_NonUUIDPrimaryKeyErrors(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharebadpk", "sales")
	tenantSlug := "sharebadpk"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	badPKModel := *model.Define("order", model.Table("sales_orders"), model.Shareable(model.ReadShare)).
		Field("id", model.Integer().Required().PrimaryKey()).
		Field("salesperson_id", model.UUID().Required())
	modelDecls := []model.ModelDeclaration{badPKModel}
	syncOrdersTable(t, engine, sess, modelDecls)

	err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{{
		Name:      "sales:order:own_only",
		AppliesTo: "sales:order:read",
		Condition: "record.salesperson_id = current_user.contact_id",
	}})
	if err == nil {
		t.Fatal("SyncRLSPolicies() error = nil, want an error for a non-UUID primary key")
	}
}

// Anonymous activity contexts set an empty current_user_id. NULLIF before the UUID cast
// must hide shared rows without raising a query error.
func TestSyncShareWidening_EmptyCurrentUserIDDoesNotError(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "shareemptyuser", "sales")
	tenantSlug := "shareemptyuser"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare)}
	syncOrdersTable(t, engine, sess, modelDecls)
	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
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

// record_shares stores one UUID per record; matching only part of a composite key would
// grant access too broadly.
func TestSyncShareWidening_CompositePrimaryKeyErrors(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharecompositepk", "sales")
	tenantSlug := "sharecompositepk"
	bootstrapRecordShares(t, adminConn, tenantSlug)

	compositePKModel := *model.Define("order", model.Table("sales_orders"), model.Shareable(model.ReadShare)).
		Field("id", model.UUID().Required().PrimaryKey()).
		Field("salesperson_id", model.UUID().Required().PrimaryKey())
	modelDecls := []model.ModelDeclaration{compositePKModel}
	syncOrdersTable(t, engine, sess, modelDecls)

	err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{{
		Name:      "sales:order:own_only",
		AppliesTo: "sales:order:read",
		Condition: "record.salesperson_id = current_user.contact_id",
	}})
	if err == nil {
		t.Fatal("SyncRLSPolicies() error = nil, want an error for a composite primary key")
	}
}

// Share policy installation creates its required share table even when provisioning
// omitted it.
func TestSyncShareWidening_CreatesRecordSharesTableIfMissing(t *testing.T) {
	sess, engine, adminConn := setupTenantSchemaForModule(t, "sharenoprovision", "sales")
	tenantSlug := "sharenoprovision"
	// Deliberately no bootstrapRecordShares(t, adminConn, tenantSlug) call
	// here — this is the whole point of the test.

	modelDecls := []model.ModelDeclaration{shareableOrdersModel(model.ReadShare)}
	syncOrdersTable(t, engine, sess, modelDecls)

	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{ownOnlyPolicy()}); err != nil {
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

// No share permissions means no widening SQL, so primary-key validation is unnecessary.
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

	err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{{
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

// Module sync locks differ within a tenant, so first-use share-table creation needs its
// own tenant-scoped lock.
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
			sess, err := pool.BeginSync(t.Context(), tenantID, tenantSlug, moduleName, testManifest("1.0.0"))
			if err != nil {
				errs <- fmt.Errorf("%s: BeginSync: %w", moduleName, err)
				return
			}
			defer func() { _ = sess.Close(t.Context()) }()

			tableName := fmt.Sprintf("orders_%d", i)
			modelDecls := []model.ModelDeclaration{
				*model.Define("order", model.Table(tableName), model.Shareable(model.ReadShare)).
					Field("id", model.UUID().Required().PrimaryKey()).
					Field("salesperson_id", model.UUID().Required()),
			}
			changes, err := engine.Diff(t.Context(), sess, modelDecls, nil)
			if err != nil {
				errs <- fmt.Errorf("%s: Diff: %w", moduleName, err)
				return
			}
			if _, _, err := engine.ExecuteAccepted(t.Context(), sess, modelDecls, changes, nil); err != nil {
				errs <- fmt.Errorf("%s: Execute: %w", moduleName, err)
				return
			}

			policy := manifest.Policy{
				Name:      moduleName + ":order:own_only",
				AppliesTo: moduleName + ":order:read",
				Condition: "record.salesperson_id = current_user.contact_id",
			}
			if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{policy}); err != nil {
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

// Exercise duplicate cleanup on an existing table without its unique share index.
func TestEnsureRecordSharesTable_DeduplicatesAnExistingTable(t *testing.T) {
	sess, engine, conn := setupTenantSchemaForModule(t, "sharelegacy", "sharelegacymod")
	schema := quoteIdent("tenant_sharelegacy")

	if _, err := conn.Exec(`CREATE TABLE ` + schema + `.record_shares (
	    id                   UUID PRIMARY KEY DEFAULT uuidv7(),
	    model                TEXT NOT NULL,
	    record_id            UUID NOT NULL,
	    shared_with_user_id  UUID NOT NULL,
	    permission           TEXT NOT NULL CHECK (permission IN ('read', 'write')),
	    shared_by            UUID NOT NULL,
	    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    expires_at           TIMESTAMPTZ
	)`); err != nil {
		t.Fatalf("create legacy record_shares: %v", err)
	}
	if _, err := conn.Exec(`CREATE INDEX idx_record_shares_lookup ON ` + schema + `.record_shares(model, record_id, shared_with_user_id)`); err != nil {
		t.Fatalf("create legacy lookup index: %v", err)
	}
	const recordID, userID = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	for i, id := range []string{"aaaaaaaa-0000-0000-0000-000000000001", "aaaaaaaa-0000-0000-0000-000000000002"} {
		if _, err := conn.Exec(
			`INSERT INTO `+schema+`.record_shares (id, model, record_id, shared_with_user_id, permission, shared_by, created_at)
			 VALUES ($1, 'sales.order', $2, $3, 'read', gen_random_uuid(), NOW() - make_interval(hours => $4))`,
			id, recordID, userID, 10-i,
		); err != nil {
			t.Fatalf("seed duplicate %s: %v", id, err)
		}
	}

	if err := engine.ensureRecordSharesTable(t.Context(), sess); err != nil {
		t.Fatalf("ensureRecordSharesTable() over a table holding duplicates error: %v", err)
	}

	var remaining []string
	rows, err := conn.Query(`SELECT id FROM ` + schema + `.record_shares`)
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		remaining = append(remaining, id)
	}
	if len(remaining) != 1 || remaining[0] != "aaaaaaaa-0000-0000-0000-000000000002" {
		t.Errorf("remaining rows = %v, want only the newest", remaining)
	}

	var lookupExists, uniqueExists bool
	if err := conn.QueryRow(`SELECT
		EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'tenant_sharelegacy' AND indexname = 'idx_record_shares_lookup'),
		EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'tenant_sharelegacy' AND indexname = 'idx_record_shares_unique')`).Scan(&lookupExists, &uniqueExists); err != nil {
		t.Fatalf("check indexes: %v", err)
	}
	if lookupExists || !uniqueExists {
		t.Errorf("lookup index exists = %v, unique index exists = %v, want false, true", lookupExists, uniqueExists)
	}

	if err := engine.ensureRecordSharesTable(t.Context(), sess); err != nil {
		t.Errorf("second ensureRecordSharesTable() error: %v", err)
	}
}

func syncOrdersWithPolicies(t *testing.T, tenant string, policies []manifest.Policy) (adminConn *sql.DB, schemaName, table string, engine *SchemaDiffEngine, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration) {
	t.Helper()

	sess, engine = setupTenantSchema(t, tenant)
	adminConn, _ = openTestPool(t, 5*time.Second)

	modelDecls = []model.ModelDeclaration{ordersModel()}
	changes, err := engine.Diff(t.Context(), sess, modelDecls, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(t.Context(), sess, modelDecls, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, policies); err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v", err)
	}

	schemaName = "tenant_" + tenant
	return adminConn, schemaName, quoteIdent(schemaName) + "." + quoteIdent("sales_orders"), engine, sess, modelDecls
}

func policyKinds(t *testing.T, db *sql.DB, schemaName string) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT policyname, permissive FROM pg_policies WHERE schemaname = $1 AND tablename = 'sales_orders'`, schemaName)
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	defer rows.Close()
	kinds := map[string]string{}
	for rows.Next() {
		var name, permissive string
		if err := rows.Scan(&name, &permissive); err != nil {
			t.Fatalf("scan: %v", err)
		}
		kinds[name] = permissive
	}
	return kinds
}

func TestSyncRLSPolicies_RestrictivePolicyMustAlsoHold(t *testing.T) {
	adminConn, schemaName, table, _, _, _ := syncOrdersWithPolicies(t, "rlsrestrictive", []manifest.Policy{
		{Name: "sales:order:own_only", AppliesTo: "sales:order:read", Condition: "record.salesperson_id = current_user.contact_id"},
		{Name: "sales:order:verified_only", AppliesTo: "sales:order:read", Condition: "user_has_role('verified')", Combine: "AND"},
	})

	if kinds := policyKinds(t, adminConn, schemaName); kinds["sales:order:own_only"] != "PERMISSIVE" || kinds["sales:order:verified_only"] != "RESTRICTIVE" {
		t.Fatalf("installed policy kinds = %v, want own_only permissive and verified_only restrictive", kinds)
	}

	repID := "22222222-2222-2222-2222-222222222222"
	insertRow(t, adminConn, table, repID)
	insertRow(t, adminConn, table, "33333333-3333-3333-3333-333333333333")
	readerConn := openTestRLSReader(t, adminConn, schemaName, table)

	tests := []struct {
		name     string
		user     string
		roles    string
		wantRows int
	}{
		{"owner holding the required role", repID, "verified", 1},
		{"owner without the required role", repID, "", 0},
		{"unrelated user holding the required role", "55555555-5555-5555-5555-555555555555", "verified", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if rows := countVisibleRows(t, readerConn, schemaName, table, tc.user, tc.roles); rows != tc.wantRows {
				t.Errorf("session saw %d rows, want %d", rows, tc.wantRows)
			}
		})
	}
}

func TestSyncRLSPolicies_RestrictiveOnlyAdmitsNothing(t *testing.T) {
	adminConn, schemaName, table, _, _, _ := syncOrdersWithPolicies(t, "rlsrestrictiveonly", []manifest.Policy{
		{Name: "sales:order:verified_only", AppliesTo: "sales:order:read", Condition: "user_has_role('verified')", Combine: "AND"},
	})
	insertRow(t, adminConn, table, "22222222-2222-2222-2222-222222222222")
	readerConn := openTestRLSReader(t, adminConn, schemaName, table)

	if rows := countVisibleRows(t, readerConn, schemaName, table, "22222222-2222-2222-2222-222222222222", "verified"); rows != 0 {
		t.Errorf("a session holding the required role saw %d rows, want 0 without a permissive policy", rows)
	}
}

func TestSyncRLSPolicies_ChangedCombineIsReapplied(t *testing.T) {
	policy := manifest.Policy{Name: "sales:order:verified_only", AppliesTo: "sales:order:read", Condition: "user_has_role('verified')", Combine: "AND"}
	adminConn, schemaName, _, engine, sess, modelDecls := syncOrdersWithPolicies(t, "rlscombinechange", []manifest.Policy{policy})
	if got := policyKinds(t, adminConn, schemaName)[policy.Name]; got != "RESTRICTIVE" {
		t.Fatalf("after the first sync the policy is %s, want RESTRICTIVE", got)
	}

	policy.Combine = ""
	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{policy}); err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v", err)
	}
	if got := policyKinds(t, adminConn, schemaName)[policy.Name]; got != "PERMISSIVE" {
		t.Errorf("after dropping combine the policy is %s, want PERMISSIVE", got)
	}

	policy.Combine = "AND"
	if err := engine.SyncRLSPolicies(t.Context(), sess, modelDecls, []manifest.Policy{policy}); err != nil {
		t.Fatalf("SyncRLSPolicies() error: %v", err)
	}
	if got := policyKinds(t, adminConn, schemaName)[policy.Name]; got != "RESTRICTIVE" {
		t.Errorf("after restoring combine the policy is %s, want RESTRICTIVE", got)
	}
}
