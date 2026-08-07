package schema

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// localSchemaSyncDSN points directly at the compose.dev.yml Postgres
// instance (localhost:55432), bypassing PgBouncer — schema sync needs a
// session-scoped connection for advisory locks and SET search_path to
// reliably stay on one backend, which a PgBouncer transaction-pooled
// connection cannot guarantee (data-layer.md §2.7).
const localSchemaSyncDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openTestPool(t *testing.T, lockAcquireTimeout time.Duration) (*sql.DB, *SchemaSyncPool) {
	t.Helper()

	conn, err := db.New(localSchemaSyncDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localSchemaSyncDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	pool := NewPool(conn, lockAcquireTimeout)
	if err := pool.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return conn, pool
}

func testManifest(version string) *manifest.Manifest {
	extendsModule := "contacts"
	return &manifest.Manifest{
		Version: version,
		Schema: manifest.SchemaConfig{
			OwnedModels:       []string{"sales.order", "sales.order_line"},
			ExtendsModule:     &extendsModule,
			ExtendsModels:     []string{"contacts.contact"},
			HasDataMigrations: true,
		},
	}
}

func TestBeginSyncOpensAndClosesSession(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)

	m := testManifest("1.0.0")
	sess, err := pool.BeginSync(context.Background(), "11111111-1111-1111-1111-111111111111", "beginsynctest", "sales", m)
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}

	if got := sess.OwnedModels(); len(got) != 2 || got[0] != "sales.order" {
		t.Errorf("OwnedModels() = %v, want manifest's owned_models", got)
	}
	if got := sess.ExtendsModule(); got == nil || *got != "contacts" {
		t.Errorf("ExtendsModule() = %v, want \"contacts\"", got)
	}
	if got := sess.ExtendsModels(); len(got) != 1 || got[0] != "contacts.contact" {
		t.Errorf("ExtendsModels() = %v, want [contacts.contact]", got)
	}
	if !sess.HasDataMigrations() {
		t.Error("HasDataMigrations() = false, want true")
	}
	if got := sess.ModuleVersion(); got != "1.0.0" {
		t.Errorf("ModuleVersion() = %q, want \"1.0.0\"", got)
	}

	if err := sess.Close(context.Background()); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestBeginSyncSerializesSameTenantModule(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)

	m := testManifest("1.0.0")
	first, err := pool.BeginSync(context.Background(), "22222222-2222-2222-2222-222222222222", "locktest", "sales", m)
	if err != nil {
		t.Fatalf("first BeginSync() error: %v", err)
	}
	defer func() { _ = first.Close(context.Background()) }()

	// A second pool sharing the same underlying connection, but with a short
	// lock-acquire timeout, so a blocked BeginSync fails fast instead of
	// hanging the test.
	shortPool := NewPool(conn, 300*time.Millisecond)
	_, err = shortPool.BeginSync(context.Background(), "22222222-2222-2222-2222-222222222222", "locktest", "sales", m)
	if err == nil {
		t.Fatal("second BeginSync() for the same (tenant, module) while the first is open: expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out waiting for schema sync lock") {
		t.Errorf("second BeginSync() error = %v, want a lock-timeout error", err)
	}

	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("first.Close() error: %v", err)
	}

	// Now that the lock is released, the same pair should succeed.
	second, err := pool.BeginSync(context.Background(), "22222222-2222-2222-2222-222222222222", "locktest", "sales", m)
	if err != nil {
		t.Fatalf("BeginSync() after lock release: unexpected error: %v", err)
	}
	if err := second.Close(context.Background()); err != nil {
		t.Errorf("second.Close() error: %v", err)
	}
}

func TestNeedsSync(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)

	const tenantID = "33333333-3333-3333-3333-333333333333"
	const tenantSlug = "needssynctest"
	const moduleName = "sales"

	// Reset any state a previous run of this test left behind.
	cleanup, err := db.New(localSchemaSyncDSN)
	if err != nil {
		t.Fatalf("db.New() for cleanup connection: %v", err)
	}
	defer func() { _ = cleanup.Close() }()
	if _, err := cleanup.Exec("DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2", tenantID, moduleName); err != nil {
		t.Fatalf("reset module_schema_versions: %v", err)
	}

	m := testManifest("1.0.0")

	// No row yet — sync is needed.
	sess, err := pool.BeginSync(context.Background(), tenantID, tenantSlug, moduleName, m)
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	needs, err := sess.NeedsSync(context.Background())
	if err != nil {
		t.Fatalf("NeedsSync() error: %v", err)
	}
	if !needs {
		t.Error("NeedsSync() with no existing row: got false, want true")
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// current_version matches the manifest's version — sync is skipped.
	if _, err := cleanup.Exec(
		"INSERT INTO system.module_schema_versions (tenant_id, module_name, current_version) VALUES ($1, $2, $3)",
		tenantID, moduleName, "1.0.0",
	); err != nil {
		t.Fatalf("seed module_schema_versions: %v", err)
	}

	sess, err = pool.BeginSync(context.Background(), tenantID, tenantSlug, moduleName, m)
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	needs, err = sess.NeedsSync(context.Background())
	if err != nil {
		t.Fatalf("NeedsSync() error: %v", err)
	}
	if needs {
		t.Error("NeedsSync() with matching current_version: got true, want false")
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// current_version differs from the manifest's version — sync is needed.
	if _, err := cleanup.Exec(
		"UPDATE system.module_schema_versions SET current_version = $1 WHERE tenant_id = $2 AND module_name = $3",
		"0.9.0", tenantID, moduleName,
	); err != nil {
		t.Fatalf("update module_schema_versions: %v", err)
	}

	sess, err = pool.BeginSync(context.Background(), tenantID, tenantSlug, moduleName, m)
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	defer func() { _ = sess.Close(context.Background()) }()
	needs, err = sess.NeedsSync(context.Background())
	if err != nil {
		t.Fatalf("NeedsSync() error: %v", err)
	}
	if !needs {
		t.Error("NeedsSync() with differing current_version: got false, want true")
	}
}
