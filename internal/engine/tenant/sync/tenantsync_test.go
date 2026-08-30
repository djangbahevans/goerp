package tenantsync

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/schema's and
// internal/engine/tenant's tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func widgetModel() model.ModelDeclaration {
	return *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("sku", model.Char(40).Required()).
		Index("idx_widgets_sku", model.BTreeIndex("sku").Unique())
}

// testEnv wires a real Postgres-backed SchemaSyncPool, SchemaDiffEngine,
// and tenant.Store, all against the compose.dev.yml instance — the same
// components engine.New() constructs, so SyncAll is exercised end to end
// rather than against fakes.
type testEnv struct {
	conn        *sql.DB
	pool        *schema.SchemaSyncPool
	diffEngine  *schema.SchemaDiffEngine
	tenantStore *tenant.Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	pool := schema.NewPool(conn, 5*time.Second)
	if err := pool.Bootstrap(context.Background()); err != nil {
		t.Fatalf("schema pool Bootstrap() error: %v", err)
	}

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("tenant store Bootstrap() error: %v", err)
	}

	return &testEnv{
		conn:        conn,
		pool:        pool,
		diffEngine:  schema.NewSchemaDiffEngine(&schema.Config{}),
		tenantStore: tenantStore,
	}
}

// activeTenant creates a tenant, flips it to active (CreateTenant defaults
// to "provisioning"), and creates its tenant_{slug} Postgres schema fresh
// — the same three preconditions a real Stage 4 run assumes are already
// true by the time it starts.
func (e *testEnv) activeTenant(t *testing.T, slug string) tenant.Tenant {
	t.Helper()

	tt, err := e.tenantStore.CreateTenant(context.Background(), slug, "Test Tenant "+slug)
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	// module_schema_versions has no FK to tenants (tenant_id is a bare
	// UUID column), so it isn't cleaned up by tenants' ON DELETE CASCADE —
	// clean up both explicitly, or a stale "active" tenant from a
	// previous run keeps getting picked up by every later run's
	// ActiveTenants() (its tenant_{slug} schema is already gone by then,
	// so every such run logs an otherwise-confusing sync failure for a
	// tenant this run never created).
	t.Cleanup(func() {
		_, _ = e.conn.Exec("DELETE FROM system.module_schema_versions WHERE tenant_id = $1", tt.ID)
		_, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID)
	})
	if _, err := e.conn.Exec("UPDATE system.tenants SET status = 'active' WHERE id = $1", tt.ID); err != nil {
		t.Fatalf("mark tenant active: %v", err)
	}
	tt.Status = tenant.StatusActive

	schemaName := "tenant_" + slug
	if _, err := e.conn.Exec("DROP SCHEMA IF EXISTS " + quoteIdent(schemaName) + " CASCADE"); err != nil {
		t.Fatalf("drop tenant schema: %v", err)
	}
	if _, err := e.conn.Exec("CREATE SCHEMA " + quoteIdent(schemaName)); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.conn.Exec("DROP SCHEMA IF EXISTS " + quoteIdent(schemaName) + " CASCADE")
	})

	return *tt
}

func uniqueSlug(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("s%d", time.Now().UnixNano())
}

func loadedModule(t *testing.T, name string, decls ...model.ModelDeclaration) *module.LoadedModule {
	t.Helper()
	return &module.LoadedModule{
		Status: module.StatusSyncing,
		Manifest: manifest.Manifest{
			Name:    name,
			Version: "1.0.0",
			Schema:  manifest.SchemaConfig{OwnedModels: []string{"sales.widget"}},
		},
		ModelDecls: decls,
	}
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

func TestSyncAll_CreatesTableAndRecordsSuccess(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	mod := loadedModule(t, "widgets_"+slug, widgetModel())

	if err := SyncAll(context.Background(), env.pool, env.diffEngine, env.tenantStore, []*module.LoadedModule{mod}, 0); err != nil {
		t.Fatalf("SyncAll() error: %v", err)
	}

	if !tableExists(t, env.conn, "tenant_"+slug, "widgets") {
		t.Error("expected the widgets table to have been created")
	}

	var status, currentVersion string
	err := env.conn.QueryRow(
		"SELECT current_version, schema_sync_status FROM system.module_schema_versions WHERE module_name = $1",
		mod.Manifest.Name,
	).Scan(&currentVersion, &status)
	if err != nil {
		t.Fatalf("query module_schema_versions: %v", err)
	}
	if status != "ok" {
		t.Errorf("schema_sync_status = %q, want %q", status, "ok")
	}
	if currentVersion != "1.0.0" {
		t.Errorf("current_version = %q, want %q", currentVersion, "1.0.0")
	}
}

func TestSyncAll_SkipsAlreadySyncedVersion(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	mod := loadedModule(t, "widgets_"+slug, widgetModel())

	if err := SyncAll(context.Background(), env.pool, env.diffEngine, env.tenantStore, []*module.LoadedModule{mod}, 0); err != nil {
		t.Fatalf("first SyncAll() error: %v", err)
	}
	if !tableExists(t, env.conn, "tenant_"+slug, "widgets") {
		t.Fatal("expected the widgets table to exist after the first sync")
	}

	// Drop the table Diff would otherwise recreate, then sync again at the
	// same version — if the skip check didn't work, the table would come
	// back; since it's skipped, it stays dropped.
	if _, err := env.conn.Exec(`DROP TABLE ` + quoteIdent("tenant_"+slug) + `.widgets`); err != nil {
		t.Fatalf("drop widgets table: %v", err)
	}

	if err := SyncAll(context.Background(), env.pool, env.diffEngine, env.tenantStore, []*module.LoadedModule{mod}, 0); err != nil {
		t.Fatalf("second SyncAll() error: %v", err)
	}

	if tableExists(t, env.conn, "tenant_"+slug, "widgets") {
		t.Error("expected sync to have been skipped (table should still be dropped), but it was recreated")
	}
}

func TestSyncAll_SkipsFailedModules(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	mod := loadedModule(t, "widgets_"+slug, widgetModel())
	mod.Fail("compile error")

	if err := SyncAll(context.Background(), env.pool, env.diffEngine, env.tenantStore, []*module.LoadedModule{mod}, 0); err != nil {
		t.Fatalf("SyncAll() error: %v", err)
	}

	if tableExists(t, env.conn, "tenant_"+slug, "widgets") {
		t.Error("expected no table to be created for a StatusFailed module")
	}
}

func TestSyncAll_OneTenantFailureDoesNotBlockAnother(t *testing.T) {
	env := newTestEnv(t)
	goodSlug := uniqueSlug(t)
	env.activeTenant(t, goodSlug)

	badSlug := uniqueSlug(t)
	badTenant, err := env.tenantStore.CreateTenant(context.Background(), badSlug, "No Schema Tenant")
	if err != nil {
		t.Fatalf("CreateTenant(bad) error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.conn.Exec("DELETE FROM system.module_schema_versions WHERE tenant_id = $1", badTenant.ID)
		_, _ = env.conn.Exec("DELETE FROM system.tenants WHERE id = $1", badTenant.ID)
	})
	if _, err := env.conn.Exec("UPDATE system.tenants SET status = 'active' WHERE id = $1", badTenant.ID); err != nil {
		t.Fatalf("mark bad tenant active: %v", err)
	}
	// Deliberately no tenant_{badSlug} schema created — Diff will fail
	// against it (InspectSchema against a schema that doesn't exist).

	mod := loadedModule(t, "widgets_"+goodSlug+"_"+badSlug, widgetModel())

	if err := SyncAll(context.Background(), env.pool, env.diffEngine, env.tenantStore, []*module.LoadedModule{mod}, 0); err != nil {
		t.Fatalf("SyncAll() error: %v", err)
	}

	if !tableExists(t, env.conn, "tenant_"+goodSlug, "widgets") {
		t.Error("expected the good tenant's widgets table to have been created despite the bad tenant's failure")
	}
}

func TestSyncAll_UnknownActiveTenantsErrorSurfaces(t *testing.T) {
	env := newTestEnv(t)
	// Close the connection so ActiveTenants itself fails, exercising the
	// one error path SyncAll actually returns (as opposed to a per-tenant
	// sync failure, which never surfaces as a returned error).
	_ = env.conn.Close()

	mod := loadedModule(t, "irrelevant", widgetModel())
	err := SyncAll(context.Background(), env.pool, env.diffEngine, env.tenantStore, []*module.LoadedModule{mod}, 0)
	if err == nil {
		t.Fatal("expected an error when active tenants can't be enumerated")
	}
}

// findResult locates id's entry in a []tenant.Tenant or []TenantSyncResult
// by tenant ID — SyncModule's result also carries whatever other tenants
// happen to be active in the shared dev database (leftover fixtures from
// other packages' tests, e.g. schema/pool_test.go's, or concurrent test
// runs), so assertions below check that the tenants this test created
// ended up in the expected bucket rather than asserting exact slice
// contents.
func findSucceeded(succeeded []tenant.Tenant, id string) bool {
	for _, t := range succeeded {
		if t.ID == id {
			return true
		}
	}
	return false
}

func findFailed(failed []TenantSyncResult, id string) *TenantSyncResult {
	for i, r := range failed {
		if r.Tenant.ID == id {
			return &failed[i]
		}
	}
	return nil
}

func TestSyncModule_CreatesTableAndReturnsSucceeded(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)

	mod := loadedModule(t, "widgets_"+slug, widgetModel())

	result, err := SyncModule(context.Background(), env.pool, env.diffEngine, env.tenantStore, mod, 0)
	if err != nil {
		t.Fatalf("SyncModule() error: %v", err)
	}

	if !findSucceeded(result.Succeeded, tt.ID) {
		t.Errorf("Succeeded = %+v, want it to contain tenant %v", result.Succeeded, tt.ID)
	}
	if r := findFailed(result.Failed, tt.ID); r != nil {
		t.Errorf("Failed unexpectedly contains tenant %v: %+v", tt.ID, r)
	}
	if !tableExists(t, env.conn, "tenant_"+slug, "widgets") {
		t.Error("expected the widgets table to have been created")
	}
}

func TestSyncModule_OneTenantFailureIsReportedWithoutBlockingAnother(t *testing.T) {
	env := newTestEnv(t)
	goodSlug := uniqueSlug(t)
	goodTenant := env.activeTenant(t, goodSlug)

	badSlug := uniqueSlug(t)
	badTenant, err := env.tenantStore.CreateTenant(context.Background(), badSlug, "No Schema Tenant")
	if err != nil {
		t.Fatalf("CreateTenant(bad) error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = env.conn.Exec("DELETE FROM system.module_schema_versions WHERE tenant_id = $1", badTenant.ID)
		_, _ = env.conn.Exec("DELETE FROM system.tenants WHERE id = $1", badTenant.ID)
	})
	if _, err := env.conn.Exec("UPDATE system.tenants SET status = 'active' WHERE id = $1", badTenant.ID); err != nil {
		t.Fatalf("mark bad tenant active: %v", err)
	}
	// Deliberately no tenant_{badSlug} schema created — Diff will fail
	// against it (InspectSchema against a schema that doesn't exist).

	mod := loadedModule(t, "widgets_"+goodSlug+"_"+badSlug, widgetModel())

	result, err := SyncModule(context.Background(), env.pool, env.diffEngine, env.tenantStore, mod, 0)
	if err != nil {
		t.Fatalf("SyncModule() error: %v", err)
	}

	if !findSucceeded(result.Succeeded, goodTenant.ID) {
		t.Errorf("Succeeded = %+v, want it to contain the good tenant %v", result.Succeeded, goodTenant.ID)
	}
	badResult := findFailed(result.Failed, badTenant.ID)
	if badResult == nil {
		t.Fatalf("Failed = %+v, want it to contain the bad tenant %v", result.Failed, badTenant.ID)
	}
	if badResult.Err == nil {
		t.Error("expected the bad tenant's result to carry a non-nil Err")
	}
	if !tableExists(t, env.conn, "tenant_"+goodSlug, "widgets") {
		t.Error("expected the good tenant's widgets table to have been created despite the bad tenant's failure")
	}
}

func TestSyncModule_UnknownActiveTenantsErrorSurfaces(t *testing.T) {
	env := newTestEnv(t)
	// Close the connection so ActiveTenants itself fails, exercising the
	// one error path SyncModule actually returns (as opposed to a
	// per-tenant sync failure, which is reported via SyncModuleResult
	// instead).
	_ = env.conn.Close()

	mod := loadedModule(t, "irrelevant", widgetModel())
	_, err := SyncModule(context.Background(), env.pool, env.diffEngine, env.tenantStore, mod, 0)
	if err == nil {
		t.Fatal("expected an error when active tenants can't be enumerated")
	}
}

// TestSyncOne_CreatesTable guards SyncOne directly against a tenant
// SyncAll/ActiveTenants would never see — goerp#149's provisioning
// workflow needs it for exactly this: a tenant still StatusProvisioning,
// which ActiveTenants filters out entirely.
func TestSyncOne_CreatesTable(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug) // schema setup only — status doesn't matter to SyncOne itself
	tt.Status = tenant.StatusProvisioning

	mod := loadedModule(t, "widgets_"+slug, widgetModel())

	if err := SyncOne(context.Background(), env.pool, env.diffEngine, tt, mod, nil); err != nil {
		t.Fatalf("SyncOne() error: %v", err)
	}

	if !tableExists(t, env.conn, "tenant_"+slug, "widgets") {
		t.Error("expected the widgets table to have been created")
	}
}
