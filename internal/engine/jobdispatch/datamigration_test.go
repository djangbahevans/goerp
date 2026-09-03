package jobdispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
	"github.com/tetratelabs/wazero"
	"github.com/vmihailenco/msgpack/v5"
)

func testJob(args jobqueue.WASMJobArgs) *river.Job[jobqueue.WASMJobArgs] {
	return &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{}, Args: args}
}

// localSchemaSyncDSN/jobsTestDSN match the established constants in
// internal/engine/schema's own session_test.go and
// internal/engine/eventdelivery's own worker_test.go respectively.
const (
	localSchemaSyncDSN = "postgres://goerp:dev@localhost:55432/goerp"
	jobsTestDSN        = "postgres://goerp:dev@localhost:6432/goerp"
)

const migrationTestModuleName = "datamigmodule"

func openTestSchemaSyncPool(t *testing.T) (*sql.DB, *schema.SchemaSyncPool) {
	t.Helper()
	conn, err := db.New(localSchemaSyncDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localSchemaSyncDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	pool := schema.NewPool(conn, 5*time.Second)
	if err := pool.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}
	return conn, pool
}

func newTestRiverClient(t *testing.T) *river.Client[pgx.Tx] {
	t.Helper()
	ctx := context.Background()

	pgxPool, err := pgxpool.New(ctx, jobsTestDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err := pgxPool.Ping(ctx); err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", jobsTestDSN, err)
	}
	t.Cleanup(pgxPool.Close)

	if err := jobqueue.Migrate(ctx, pgxPool); err != nil {
		t.Fatalf("jobqueue.Migrate: %v", err)
	}

	client, err := river.NewClient(riverpgxv5.New(pgxPool), &river.Config{})
	if err != nil {
		t.Fatalf("river.NewClient: %v", err)
	}
	return client
}

// newDataMigrationModule builds a real StatusReady *module.LoadedModule
// backed by handleJobEchoModule (zero status on an empty payload,
// non-zero on any other length) — this package's own tests that call
// Work() against it construct their own WASMJobArgs by hand with Payload
// left at its zero value, rather than reading back what
// EnqueueApplicableDataMigration actually inserted (which does now carry
// a real msgpack-encoded model.MigrationJobPayload, never nil — see
// realfixture_test.go for tests that exercise that real payload, against
// the real compiled fixture that can actually decode it). Declares
// migrations but deliberately no JobTypes — proving dispatch for a
// migration handler doesn't depend on it being separately declared there
// too.
func newDataMigrationModule(t *testing.T, migrations []model.DataMigration, version string) *module.LoadedModule {
	t.Helper()
	ctx := context.Background()

	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	compiled, err := rt.CompileModule(ctx, handleJobEchoModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	pool := wasm.NewInstancePool(migrationTestModuleName, compiled, rt, wasm.PoolConfig{
		MaxSize:       2,
		BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), time.Second) })

	return &module.LoadedModule{
		Status: module.StatusReady,
		Pool:   pool,
		Manifest: manifest.Manifest{
			Name:    migrationTestModuleName,
			Type:    "standard",
			Version: version,
		},
		DataMigrations: migrations,
	}
}

func newTestRegistry(t *testing.T, mod *module.LoadedModule) *registry.ModuleRegistry {
	t.Helper()
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{mod.Manifest.Name: mod}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}
	return reg
}

// seedSyncedRow creates the module_schema_versions row DDL sync's own
// RecordSyncSuccess would leave behind before any data migration job is
// ever enqueued for a (tenant, module) pair (engine-internals.md §2 Stage
// 4 steps 25/26 — DDL always syncs first) — AdvanceDataMigrationVersion
// requires this row to already exist.
func seedSyncedRow(t *testing.T, pool *schema.SchemaSyncPool, tenantID, moduleName, version string) {
	t.Helper()
	sess, err := pool.BeginSync(context.Background(), tenantID, "sl_"+tenantID[:8], moduleName, &manifest.Manifest{Version: version})
	if err != nil {
		t.Fatalf("BeginSync() error: %v", err)
	}
	if err := sess.RecordSyncSuccess(context.Background()); err != nil {
		t.Fatalf("RecordSyncSuccess() error: %v", err)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("session Close() error: %v", err)
	}
}

// openJobsConn opens its own *sql.DB against jobsTestDSN — deliberately
// not the schema-sync conn openTestSchemaSyncPool returns: they're
// different databases (config.Config's DBSchemaSyncDSN vs the primary/job
// queue DSN), and even where a DSN happened to coincide, a connection
// that has run a SchemaSyncSession may have a pooled connection sitting
// with search_path still set to a tenant schema rather than public, where
// river_job lives.
func openJobsConn(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.New(jobsTestDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", jobsTestDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// cleanupRiverJobsForTenant deletes every wasm_job row this test's own
// EnqueueApplicableDataMigration calls inserted for tenantID, keeping the
// shared dev river_job table from accumulating test rows across runs —
// same rationale as eventdelivery's own uniqueEventID cleanup.
func cleanupRiverJobsForTenant(t *testing.T, conn *sql.DB, tenantID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM river_job WHERE kind = 'wasm_job' AND args->>'tenant_id' = $1`, tenantID)
	})
}

func countRiverJobsForHandler(t *testing.T, conn *sql.DB, moduleName, handler, tenantID string) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(
		`SELECT count(*) FROM river_job WHERE kind = 'wasm_job' AND args->>'module_name' = $1 AND args->>'job_type' = $2 AND args->>'tenant_id' = $3`,
		moduleName, handler, tenantID,
	).Scan(&count); err != nil {
		t.Fatalf("query river_job: %v", err)
	}
	return count
}

func TestWork_DataMigrationHandlerNotDeclaredReturnsError(t *testing.T) {
	mod := newDataMigrationModule(t, nil, "1.0.0")
	w := &Worker{ModuleRegistry: newTestRegistry(t, mod)}

	err := runWork(t, w, jobqueue.WASMJobArgs{
		ModuleName:      migrationTestModuleName,
		JobType:         "not_a_declared_migration",
		IsDataMigration: true,
	})
	if err == nil {
		t.Fatal("expected an error for a data migration handler not declared by the module")
	}
}

func TestWork_DataMigrationHandlerDeclaredSucceedsWithoutJobTypesEntry(t *testing.T) {
	conn, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	_, tenantStore := newTestTenantStore(t)
	tt := newFixtureTenant(t, conn, tenantStore)
	tenantID := tt.ID

	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, migrationTestModuleName)
	})
	mod := newDataMigrationModule(t, []model.DataMigration{
		{FromVersion: "< 1.0.0", ToVersion: ">= 1.0.0", Handler: "backfill"},
	}, "1.0.0")
	w := &Worker{ModuleRegistry: newTestRegistry(t, mod), SchemaSyncPool: syncPool, Runtime: newTestWasmRuntime(t), TenantStore: tenantStore}
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.0.0")

	ctx := rivertest.WorkContext(context.Background(), riverClient)
	err := w.Work(ctx, testJob(jobqueue.WASMJobArgs{
		ModuleName:         migrationTestModuleName,
		JobType:            "backfill",
		TenantID:           tenantID,
		IsDataMigration:    true,
		MigrationToVersion: "1.0.0",
	}))
	if err != nil {
		t.Fatalf("Work() error: %v", err)
	}
}

func TestEnqueueApplicableDataMigration_EnqueuesOnlyFirstApplicable(t *testing.T) {
	conn, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	jobsConn := openJobsConn(t)

	tenantID := uuid.NewString()
	cleanupRiverJobsForTenant(t, jobsConn, tenantID)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, migrationTestModuleName)
	})
	mod := newDataMigrationModule(t, []model.DataMigration{
		{FromVersion: "< 1.4.0", ToVersion: ">= 1.4.0", Handler: "backfill_a"},
		{FromVersion: "< 1.5.0", ToVersion: ">= 1.5.0", Handler: "backfill_b"},
	}, "1.5.0")
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.5.0")

	if err := EnqueueApplicableDataMigration(context.Background(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}

	if got := countRiverJobsForHandler(t, jobsConn, migrationTestModuleName, "backfill_a", tenantID); got != 1 {
		t.Errorf("backfill_a jobs = %d, want 1 (the first applicable handler)", got)
	}
	if got := countRiverJobsForHandler(t, jobsConn, migrationTestModuleName, "backfill_b", tenantID); got != 0 {
		t.Errorf("backfill_b jobs = %d, want 0 (not enqueued until backfill_a succeeds)", got)
	}
}

// TestEnqueueApplicableDataMigration_PayloadCarriesVersionBoundsAndHandler
// guards the wire contract engine.DispatchDataMigration decodes on the
// module's own side (sdk/go/model.MigrationJobPayload) — a regression
// here would silently break every real handler's MigrationContext
// without failing any WASM-side dispatch test, since those construct
// their own payload fixtures directly rather than through this function.
func TestEnqueueApplicableDataMigration_PayloadCarriesVersionBoundsAndHandler(t *testing.T) {
	_, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	jobsConn := openJobsConn(t)

	tenantID := uuid.NewString()
	cleanupRiverJobsForTenant(t, jobsConn, tenantID)
	mod := newDataMigrationModule(t, []model.DataMigration{
		{FromVersion: "< 1.4.0", ToVersion: ">= 1.4.0", Handler: "backfill_a"},
	}, "1.4.0")
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.4.0")

	if err := EnqueueApplicableDataMigration(context.Background(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}

	var argsJSON []byte
	if err := jobsConn.QueryRow(
		`SELECT args FROM river_job WHERE kind = 'wasm_job' AND args->>'module_name' = $1 AND args->>'job_type' = $2 AND args->>'tenant_id' = $3`,
		migrationTestModuleName, "backfill_a", tenantID,
	).Scan(&argsJSON); err != nil {
		t.Fatalf("query enqueued job args: %v", err)
	}

	var args jobqueue.WASMJobArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		t.Fatalf("unmarshal WASMJobArgs: %v", err)
	}
	if args.MigrationFromVersion != "0.0.0" {
		t.Errorf("MigrationFromVersion = %q, want 0.0.0 (this tenant's fresh watermark)", args.MigrationFromVersion)
	}
	if args.MigrationToVersion != "1.4.0" {
		t.Errorf("MigrationToVersion = %q, want 1.4.0", args.MigrationToVersion)
	}

	var payload model.MigrationJobPayload
	if err := msgpack.Unmarshal(args.Payload, &payload); err != nil {
		t.Fatalf("unmarshal Payload as model.MigrationJobPayload: %v", err)
	}
	if payload.Handler != "backfill_a" {
		t.Errorf("payload.Handler = %q, want backfill_a", payload.Handler)
	}
	if payload.TenantID != tenantID {
		t.Errorf("payload.TenantID = %q, want %q", payload.TenantID, tenantID)
	}
	if payload.FromVersion != "0.0.0" {
		t.Errorf("payload.FromVersion = %q, want 0.0.0", payload.FromVersion)
	}
	if payload.ToVersion != "1.4.0" {
		t.Errorf("payload.ToVersion = %q, want 1.4.0", payload.ToVersion)
	}
}

// TestEnqueueApplicableDataMigration_TenantNotYetSyncedIsNoop guards
// against enqueueing a migration job for a tenant whose schema sync
// hasn't actually reached mod.Manifest.Version yet — e.g. a startup sweep
// racing a sync still in flight, or one that failed. Running the handler
// here would mean it executes against schema DDL sync hasn't applied.
func TestEnqueueApplicableDataMigration_TenantNotYetSyncedIsNoop(t *testing.T) {
	conn, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	jobsConn := openJobsConn(t)

	tenantID := uuid.NewString()
	cleanupRiverJobsForTenant(t, jobsConn, tenantID)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, migrationTestModuleName)
	})
	mod := newDataMigrationModule(t, []model.DataMigration{
		{FromVersion: "< 1.5.0", ToVersion: ">= 1.5.0", Handler: "backfill_a"},
	}, "1.5.0")
	// Still synced to the OLD version — this tenant's DDL sync to 1.5.0
	// hasn't landed (or failed) yet.
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.4.0")

	if err := EnqueueApplicableDataMigration(context.Background(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}
	if got := countRiverJobsForHandler(t, jobsConn, migrationTestModuleName, "backfill_a", tenantID); got != 0 {
		t.Errorf("backfill_a jobs = %d, want 0 (tenant not yet synced to the target version)", got)
	}
}

func TestEnqueueApplicableDataMigration_NoneApplicableIsNoop(t *testing.T) {
	conn, syncPool := openTestSchemaSyncPool(t)
	riverClient := newTestRiverClient(t)
	jobsConn := openJobsConn(t)

	tenantID := uuid.NewString()
	cleanupRiverJobsForTenant(t, jobsConn, tenantID)
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2`, tenantID, migrationTestModuleName)
	})
	mod := newDataMigrationModule(t, []model.DataMigration{
		{FromVersion: "< 1.0.0", ToVersion: ">= 1.0.0", Handler: "backfill_old"},
	}, "1.0.0")
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.0.0")

	// Watermark already at the module's current version — nothing left
	// to apply.
	if err := syncPool.AdvanceDataMigrationVersion(context.Background(), tenantID, migrationTestModuleName, "1.0.0"); err != nil {
		t.Fatalf("AdvanceDataMigrationVersion() error: %v", err)
	}

	if err := EnqueueApplicableDataMigration(context.Background(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}
	if got := countRiverJobsForHandler(t, jobsConn, migrationTestModuleName, "backfill_old", tenantID); got != 0 {
		t.Errorf("backfill_old jobs = %d, want 0 (already past this migration's ToVersion)", got)
	}
}

func TestWork_DataMigrationSuccess_AdvancesWatermarkAndEnqueuesNext(t *testing.T) {
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
	mod := newDataMigrationModule(t, []model.DataMigration{
		{FromVersion: "< 1.4.0", ToVersion: ">= 1.4.0", Handler: "backfill_a"},
		{FromVersion: "< 1.5.0", ToVersion: ">= 1.5.0", Handler: "backfill_b"},
	}, "1.5.0")
	w := &Worker{ModuleRegistry: newTestRegistry(t, mod), SchemaSyncPool: syncPool, Runtime: newTestWasmRuntime(t), TenantStore: tenantStore}
	seedSyncedRow(t, syncPool, tenantID, migrationTestModuleName, "1.5.0")

	if err := EnqueueApplicableDataMigration(context.Background(), riverClient, syncPool, tenantID, mod); err != nil {
		t.Fatalf("EnqueueApplicableDataMigration() error: %v", err)
	}

	ctx := rivertest.WorkContext(context.Background(), riverClient)
	err := w.Work(ctx, testJob(jobqueue.WASMJobArgs{
		ModuleName:         migrationTestModuleName,
		JobType:            "backfill_a",
		TenantID:           tenantID,
		IsDataMigration:    true,
		MigrationToVersion: "1.4.0",
	}))
	if err != nil {
		t.Fatalf("Work() error: %v", err)
	}

	got, err := syncPool.DataMigrationVersion(context.Background(), tenantID, migrationTestModuleName)
	if err != nil {
		t.Fatalf("DataMigrationVersion() error: %v", err)
	}
	if got != "1.4.0" {
		t.Errorf("data_migration_version = %q, want %q after backfill_a succeeds", got, "1.4.0")
	}

	if count := countRiverJobsForHandler(t, jobsConn, migrationTestModuleName, "backfill_b", tenantID); count != 1 {
		t.Errorf("backfill_b jobs = %d, want 1 (enqueued once backfill_a's watermark clears its FromVersion)", count)
	}
}
