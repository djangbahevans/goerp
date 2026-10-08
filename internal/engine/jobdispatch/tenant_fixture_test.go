package jobdispatch

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
)

// Job dispatch resolves TenantID through the tenant store, so fixtures need a persisted
// tenant rather than an arbitrary UUID.
func newTestTenantStore(t *testing.T) (*sql.DB, *tenant.Store) {
	t.Helper()
	conn, err := db.New(localSchemaSyncDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localSchemaSyncDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := tenant.NewStore(conn)
	if err := store.Bootstrap(t.Context()); err != nil {
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

	tt, err := store.CreateTenant(t.Context(), slug, "Job Dispatch Test")
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

// Use the same database for host DDL and the fixture's schema-sync pool so migration DDL
// reaches the fixture tables.
func newTestWasmRuntimeWithPrimaryDB(t *testing.T, primary *sql.DB) *wasm.Runtime {
	t.Helper()
	rt, err := wasm.New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, primary, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	rt.SetSchemaSyncDB(primary)
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}
