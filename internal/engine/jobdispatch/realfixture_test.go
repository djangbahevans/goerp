package jobdispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
)

// compileMigrationFixture compiles testdata/migrationfixture — a real Go
// module using the actual sdk/go/engine.OnDataMigration/DispatchDataMigration
// and sdk/go/model.MigrationContext, not a hand-assembled bytecode
// stand-in — to wasip1 WASM, mirroring internal/engine/loader's own
// compileRealFixture (goerp#234's established convention for this class
// of test).
func compileMigrationFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "migrationfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/migrationfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/migrationfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// newRealFixtureWorker builds a real *Worker whose ModuleRegistry has one
// StatusReady module — migrationTestModuleName, declared with migrations,
// backed by a real InstancePool for the given compiled real fixture — the
// same larger pool memory limit internal/engine/loader's own
// newRealFixtureRuntime uses (a real Go-compiled wasip1 binary's minimum
// linear memory is well past what this package's other, hand-assembled
// bytecode fixtures need), and the full host-function-registering
// wasm.New (not the bare wazero.NewRuntime newDataMigrationModule uses),
// so this exercises the real host boundary rather than assuming (as the
// hand-assembled fixtures safely can) that no host.* import will ever be
// referenced.
func newRealFixtureWorker(t *testing.T, syncPool *schema.SchemaSyncPool, wasmBytes []byte, migrations []model.DataMigration, version string) *Worker {
	t.Helper()
	ctx := context.Background()

	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

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
			},
			DataMigrations: migrations,
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}

	return &Worker{ModuleRegistry: reg, SchemaSyncPool: syncPool}
}

// loadWASMJobArgs reads back the one river_job row
// EnqueueApplicableDataMigration inserted for (moduleName, handler,
// tenantID) and decodes its args column into a real jobqueue.WASMJobArgs
// — the same row countRiverJobsForHandler counts, just decoded instead
// of counted, so Worker.Work below runs against the actual inserted
// Payload rather than a hand-built stand-in.
func loadWASMJobArgs(t *testing.T, jobsConn *sql.DB, moduleName, handler, tenantID string) jobqueue.WASMJobArgs {
	t.Helper()
	var argsJSON []byte
	if err := jobsConn.QueryRow(
		`SELECT args FROM river_job WHERE kind = 'wasm_job' AND args->>'module_name' = $1 AND args->>'job_type' = $2 AND args->>'tenant_id' = $3`,
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

// TestWork_RealCompiledFixture_DataMigrationSucceeds is the end-to-end
// counterpart to internal/engine/loader's own
// TestLoadModule_RealCompiledModule_RoundTripsSDKDeclaredData, but for
// this package's own boundary: a real Go module built on the actual SDK
// (engine.OnDataMigration, engine.DispatchDataMigration,
// model.MigrationContext.Log/RecordProgress) compiles to wasip1 WASM,
// loads through a real wasm.Runtime with every host function registered,
// and Worker.Work dispatches a real jobqueue.WASMJobArgs job into it —
// exercising the actual msgpack wire contract
// (jobdispatch.EnqueueApplicableDataMigration's engine-side encode
// against engine.DispatchDataMigration's SDK-side decode) and the actual
// WASI stdout path Log/RecordProgress write through, neither of which
// the package's other, hand-assembled-bytecode-fixture tests can verify.
func TestWork_RealCompiledFixture_DataMigrationSucceeds(t *testing.T) {
	conn, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	jobsConn := openJobsConn(t)

	tenantID := uuid.NewString()
	cleanupRiverJobsForTenant(t, jobsConn, tenantID)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, migrationTestModuleName)
	})

	wasmBytes := compileMigrationFixture(t)
	migrations := []model.DataMigration{
		{FromVersion: "< 1.0.0", ToVersion: ">= 1.0.0", Handler: "backfill_test"},
	}
	w := newRealFixtureWorker(t, syncPool, wasmBytes, migrations, "1.0.0")
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.0.0")

	mod := w.ModuleRegistry.Snapshot().Modules()[migrationTestModuleName]
	if err := EnqueueApplicableDataMigration(context.Background(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}

	args := loadWASMJobArgs(t, jobsConn, migrationTestModuleName, "backfill_test", tenantID)
	job := &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{}, Args: args}

	ctx := rivertest.WorkContext(context.Background(), riverClient)
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("Work() error: %v", err)
	}

	got, err := syncPool.DataMigrationVersion(context.Background(), tenantID, migrationTestModuleName)
	if err != nil {
		t.Fatalf("DataMigrationVersion() error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("data_migration_version = %q, want 1.0.0 after the real handler succeeds", got)
	}
}

// TestWork_RealCompiledFixture_DataMigrationHandlerErrorReturnsError
// exercises the failure path through the same real-compiled-module
// boundary: a handler returning a Go error must surface as a non-zero
// handle_job status, which Worker.Work must translate into a River-retryable
// error, not a silently-swallowed success.
func TestWork_RealCompiledFixture_DataMigrationHandlerErrorReturnsError(t *testing.T) {
	conn, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	jobsConn := openJobsConn(t)

	tenantID := uuid.NewString()
	cleanupRiverJobsForTenant(t, jobsConn, tenantID)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, migrationTestModuleName)
	})

	wasmBytes := compileMigrationFixture(t)
	migrations := []model.DataMigration{
		{FromVersion: "< 1.0.0", ToVersion: ">= 1.0.0", Handler: "failing_test"},
	}
	w := newRealFixtureWorker(t, syncPool, wasmBytes, migrations, "1.0.0")
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.0.0")

	mod := w.ModuleRegistry.Snapshot().Modules()[migrationTestModuleName]
	if err := EnqueueApplicableDataMigration(context.Background(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}

	args := loadWASMJobArgs(t, jobsConn, migrationTestModuleName, "failing_test", tenantID)
	job := &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{}, Args: args}

	ctx := rivertest.WorkContext(context.Background(), riverClient)
	if err := w.Work(ctx, job); err == nil {
		t.Fatal("Work() error = nil, want an error for a handler that returns a Go error")
	}

	got, err := syncPool.DataMigrationVersion(context.Background(), tenantID, migrationTestModuleName)
	if err != nil {
		t.Fatalf("DataMigrationVersion() error: %v", err)
	}
	if got != "0.0.0" {
		t.Errorf("data_migration_version = %q, want 0.0.0 (unchanged) after a failed handler", got)
	}
}
