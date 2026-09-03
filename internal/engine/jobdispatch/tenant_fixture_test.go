package jobdispatch

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// newTestTenantStore opens localSchemaSyncDSN (the same DSN
// openTestSchemaSyncPool already uses) and bootstraps a *tenant.Store
// against it — shared by every fixture in this package that now needs a
// real system.tenants row: Worker.Work resolves a job's tenant slug via a
// real w.TenantStore.GetByID lookup (goerp#500's own ModuleContext-wiring
// fix), so a bare random UUID with no matching row no longer works as a
// test TenantID the way it did before that lookup existed.
func newTestTenantStore(t *testing.T) (*sql.DB, *tenant.Store) {
	t.Helper()
	conn, err := db.New(localSchemaSyncDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localSchemaSyncDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := tenant.NewStore(conn)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("tenant.Store.Bootstrap: %v", err)
	}
	return conn, store
}

// newFixtureTenant creates a real tenant row under a fresh unique slug —
// callers use its ID as a test job's TenantID so w.TenantStore.GetByID
// resolves a real slug instead of erroring on a nonexistent tenant.
func newFixtureTenant(t *testing.T, conn *sql.DB, store *tenant.Store) *tenant.Tenant {
	t.Helper()
	slug := fmt.Sprintf("jobdispatch%d", time.Now().UnixNano())

	tt, err := store.CreateTenant(context.Background(), slug, "Job Dispatch Test")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID)
	})
	return tt
}

// newTestWasmRuntime builds a real *wasm.Runtime with a nil primary DB —
// safe for most fixtures in this package, which never invoke a real
// host.db.* call (that's internal/engine/wasm's own test suite's job);
// what Worker.Work actually needs from it is RegisterInstance/
// UnregisterInstance and a real (if here unused) TxLimiter, the same
// pattern realfixture_test.go's own newRealFixtureWorker already
// established for this package.
func newTestWasmRuntime(t *testing.T) *wasm.Runtime {
	t.Helper()
	return newTestWasmRuntimeWithPrimaryDB(t, nil)
}

// newTestWasmRuntimeWithPrimaryDB is newTestWasmRuntime for the one
// fixture that does need a real host.db.migration_ddl call to go all the
// way through to Postgres (TestWork_RealCompiledFixture_DataMigrationDropColumnSucceeds,
// goerp#500) — primary is registerHostDB's own connection pool, so pass
// the same *sql.DB the fixture tenant's schema/tables were created
// against.
func newTestWasmRuntimeWithPrimaryDB(t *testing.T, primary *sql.DB) *wasm.Runtime {
	t.Helper()
	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, primary, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}
