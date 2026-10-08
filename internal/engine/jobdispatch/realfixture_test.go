package jobdispatch

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
)

func compileFixture(t *testing.T, name string) []byte {
	t.Helper()

	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	if data, ok := fixtureCache[name]; ok {
		return data
	}

	wasmPath := filepath.Join(t.TempDir(), name+".wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/"+name)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/%s: %v\n%s", name, err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	fixtureCache[name] = data
	return data
}

// fixtureCache holds each compiled fixture for the test process, so the
// package builds a given fixture once rather than once per test.
var (
	fixtureMu    sync.Mutex
	fixtureCache = map[string][]byte{}
)

// Real SDK fixtures require the full host runtime and a larger memory pool than hand-
// assembled status-only fixtures.
func newRealFixtureWorker(t *testing.T, syncPool *schema.SchemaSyncPool, tenantStore *tenant.Store, wasmBytes []byte, migrations []model.DataMigration, version string) *Worker {
	t.Helper()
	return newRealFixtureWorkerWithCapabilities(t, syncPool, tenantStore, nil, wasmBytes, migrations, version, 0, nil, nil)
}

// Migration DDL fixtures need CapDBMigrationDDL, owned model declarations and a real
// primary database.
func newRealFixtureWorkerWithCapabilities(t *testing.T, syncPool *schema.SchemaSyncPool, tenantStore *tenant.Store, primaryDB *sql.DB, wasmBytes []byte, migrations []model.DataMigration, version string, caps abi.CapabilitySet, modelDecls []model.ModelDeclaration, ownedModels []string) *Worker {
	t.Helper()
	ctx := t.Context()

	rt := newTestWasmRuntimeWithPrimaryDB(t, primaryDB)

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	pool := rt.NewPool(migrationTestModuleName, compiled, wasm.PoolConfig{
		MaxSize:       2,
		BorrowTimeout: 5 * time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 5*time.Second) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		migrationTestModuleName: {
			Status: module.StatusReady,
			Pool:   pool,
			Manifest: manifest.Manifest{
				Name:    migrationTestModuleName,
				Type:    "standard",
				Version: version,
				Schema:  manifest.SchemaConfig{OwnedModels: ownedModels},
			},
			Capabilities:   caps,
			ModelDecls:     modelDecls,
			DataMigrations: migrations,
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}

	return &Worker{ModuleRegistry: reg, SchemaSyncPool: syncPool, Runtime: rt, TenantStore: tenantStore}
}

// Read persisted job arguments so worker execution verifies the enqueue path's payload.
func loadWASMJobArgs(t *testing.T, jobsConn *sql.DB, moduleName, handler, tenantID string) jobqueue.WASMJobArgs {
	t.Helper()
	var argsJSON []byte
	if err := jobsConn.QueryRow(
		`SELECT args FROM system.river_job WHERE kind = 'wasm_job' AND args->>'module_name' = $1 AND args->>'job_type' = $2 AND args->>'tenant_id' = $3`,
		moduleName, handler, tenantID,
	).Scan(&argsJSON); err != nil {
		t.Fatalf("query enqueued job args: %v", err)
	}

	var args jobqueue.WASMJobArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		t.Fatalf("unmarshal WASMJobArgs: %v", err)
	}
	return args
}

// A real SDK module covers envelope decoding and WASI log output that hand-assembled
// status fixtures cannot exercise.
func TestWork_RealCompiledFixture_DataMigrationSucceeds(t *testing.T) {
	conn, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	jobsConn := openJobsConn(t)
	_, tenantStore := newTestTenantStore(t)
	tt := newFixtureTenant(t, conn, tenantStore)
	tenantID := tt.ID

	cleanupRiverJobsForTenant(t, jobsConn, tenantID)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, migrationTestModuleName)
	})

	wasmBytes := compileFixture(t, "migrationfixture")
	migrations := []model.DataMigration{
		{FromVersion: "< 1.0.0", ToVersion: ">= 1.0.0", Handler: "backfill_test"},
	}
	w := newRealFixtureWorker(t, syncPool, tenantStore, wasmBytes, migrations, "1.0.0")
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.0.0")

	mod := w.ModuleRegistry.Snapshot().Modules()[migrationTestModuleName]
	if err := EnqueueApplicableDataMigration(t.Context(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}

	args := loadWASMJobArgs(t, jobsConn, migrationTestModuleName, "backfill_test", tenantID)
	job := &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{}, Args: args}

	ctx := rivertest.WorkContext(t.Context(), riverClient)
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("Work() error: %v", err)
	}

	got, err := syncPool.DataMigrationVersion(t.Context(), tenantID, migrationTestModuleName)
	if err != nil {
		t.Fatalf("DataMigrationVersion() error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("data_migration_version = %q, want 1.0.0 after the real handler succeeds", got)
	}
}

func TestWork_RealCompiledFixture_DataMigrationHandlerErrorReturnsError(t *testing.T) {
	conn, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	jobsConn := openJobsConn(t)
	_, tenantStore := newTestTenantStore(t)
	tt := newFixtureTenant(t, conn, tenantStore)
	tenantID := tt.ID

	cleanupRiverJobsForTenant(t, jobsConn, tenantID)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, migrationTestModuleName)
	})

	wasmBytes := compileFixture(t, "migrationfixture")
	migrations := []model.DataMigration{
		{FromVersion: "< 1.0.0", ToVersion: ">= 1.0.0", Handler: "failing_test"},
	}
	w := newRealFixtureWorker(t, syncPool, tenantStore, wasmBytes, migrations, "1.0.0")
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.0.0")

	mod := w.ModuleRegistry.Snapshot().Modules()[migrationTestModuleName]
	if err := EnqueueApplicableDataMigration(t.Context(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}

	args := loadWASMJobArgs(t, jobsConn, migrationTestModuleName, "failing_test", tenantID)
	job := &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{}, Args: args}

	ctx := rivertest.WorkContext(t.Context(), riverClient)
	if err := w.Work(ctx, job); err == nil {
		t.Fatal("Work() error = nil, want an error for a handler that returns a Go error")
	}

	got, err := syncPool.DataMigrationVersion(t.Context(), tenantID, migrationTestModuleName)
	if err != nil {
		t.Fatalf("DataMigrationVersion() error: %v", err)
	}
	if got != "0.0.0" {
		t.Errorf("data_migration_version = %q, want 0.0.0 (unchanged) after a failed handler", got)
	}
}

func TestWork_RealCompiledFixture_DataMigrationDropColumnSucceeds(t *testing.T) {
	conn, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	jobsConn := openJobsConn(t)
	_, tenantStore := newTestTenantStore(t)
	tt := newFixtureTenant(t, conn, tenantStore)
	tenantID := tt.ID

	cleanupRiverJobsForTenant(t, jobsConn, tenantID)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, migrationTestModuleName)
	})

	schemaName := "tenant_" + tt.Slug
	if _, err := conn.Exec(`CREATE SCHEMA ` + schemaName); err != nil {
		t.Fatalf("create fixture tenant schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DROP SCHEMA IF EXISTS ` + schemaName + ` CASCADE`) })
	if _, err := conn.Exec(`CREATE TABLE ` + schemaName + `.widget (id UUID PRIMARY KEY, legacy_name TEXT)`); err != nil {
		t.Fatalf("create fixture widget table: %v", err)
	}

	wasmBytes := compileFixture(t, "migrationfixture")
	migrations := []model.DataMigration{
		{FromVersion: "< 1.0.0", ToVersion: ">= 1.0.0", Handler: "drop_column_test"},
	}
	modelDecls := []model.ModelDeclaration{{
		Name: "widget",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "legacy_name", Def: model.Text()},
		},
	}}
	w := newRealFixtureWorkerWithCapabilities(t, syncPool, tenantStore, conn, wasmBytes, migrations, "1.0.0", abi.CapDBMigrationDDL, modelDecls, []string{migrationTestModuleName + ".widget"})
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.0.0")

	mod := w.ModuleRegistry.Snapshot().Modules()[migrationTestModuleName]
	if err := EnqueueApplicableDataMigration(t.Context(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}

	args := loadWASMJobArgs(t, jobsConn, migrationTestModuleName, "drop_column_test", tenantID)
	job := &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{}, Args: args}

	ctx := rivertest.WorkContext(t.Context(), riverClient)
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("Work() error: %v", err)
	}

	var exists bool
	if err := conn.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = 'widget' AND column_name = 'legacy_name'
		)`, schemaName).Scan(&exists); err != nil {
		t.Fatalf("check column existence: %v", err)
	}
	if exists {
		t.Error("legacy_name column still exists after ctx.DropColumn through a real data migration job")
	}
}
