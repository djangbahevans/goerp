// Package modeltest is the module test harness (erp-design.md §14.2,
// testing-guide.md §§1-8): it compiles the module under test to wasip1
// WASM, loads it into a real Wazero runtime, syncs its declared schema
// into a fresh Postgres tenant schema, and lets a test make HTTP-shaped
// requests against its routes and assert on the response, on emitted
// events, and on database state — all against real infrastructure
// (testing-guide.md §2: Postgres via GOERP_TEST_DB_DSN, no mocks), not a
// production-like deployment.
//
// Import only from `_test.go` files, in the module's own root package
// (the directory containing manifest.json and cmd/module).
package modeltest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
	"github.com/google/uuid"
)

// riverMigrateOnce ensures River's own tables (river_job, etc. — h.Events
// queries river_job directly) exist in the test database exactly once per
// test binary run, not once per NewHarness call — River's migrator is
// safe to call repeatedly (an advisory lock plus its own applied-version
// bookkeeping), but there's no reason to pay a round trip for it on every
// test.
var riverMigrateOnce sync.Once

// defaultTestDBDSN matches compose.dev.yml's postgres service — the same
// default host_db_test.go and its siblings already use for the engine's
// own real-Postgres test suite (testing-guide.md §2's docker-compose.test.yml
// documents the equivalent standalone setup).
const defaultTestDBDSN = "postgres://goerp:dev@localhost:55432/goerp"

const lockAcquireTimeout = 10 * time.Second

// Harness is the value modeltest.NewHarness returns. It is automatically
// cleaned up when the test finishes — no manual Close call is needed.
type Harness struct {
	t *testing.T

	TenantID string
	UserID   string

	DB     *TestDB
	Events *TestEvents

	tenantSlug string
	handler    http.Handler
	permReg    *permission.PermissionRegistry
	moduleName string
	allPerms   permission.PermissionBitfield
}

// Option configures a Harness at construction time (§4).
type Option func(*harnessConfig)

type harnessConfig struct {
	userID       string
	userPerms    []string
	fixturePaths []string
}

// WithUser makes id, with perms, the harness's default user instead of
// the ordinary all-permissions default.
func WithUser(id string, perms []string) Option {
	return func(c *harnessConfig) { c.userID = id; c.userPerms = perms }
}

// WithFixture seeds the harness from a JSON fixture file once the harness
// (and the module's schema) is ready — see h.DB.SeedFromFixture for the
// file shape.
func WithFixture(path string) Option {
	return func(c *harnessConfig) { c.fixturePaths = append(c.fixturePaths, path) }
}

// NewHarness compiles the module under test (the current package's
// sibling cmd/module, per its own manifest.json) to wasip1 WASM, loads it
// into a real Wazero runtime, creates a fresh Postgres tenant schema, and
// syncs the module's declared schema into it. The harness and its tenant
// schema are dropped automatically via t.Cleanup.
//
// Requires a reachable Postgres at GOERP_TEST_DB_DSN (default
// postgres://goerp:dev@localhost:55432/goerp, compose.dev.yml's own
// credentials) — the test skips, rather than failing, if none is
// reachable.
func NewHarness(t *testing.T, opts ...Option) *Harness {
	t.Helper()
	ctx := context.Background()

	cfg := &harnessConfig{}
	for _, o := range opts {
		o(cfg)
	}

	primaryDB := openTestPrimaryDB(t)

	moduleDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("modeltest: getwd: %v", err)
	}

	wasmBytes, manifestBytes, moduleSrcName := compileModuleUnderTest(t, ctx, moduleDir)

	rt, err := wasm.New(&config.Config{
		CompilationCache:            sharedCompilationCacheDir(),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           64 << 20,
		DBMaxConcurrentTransactions: 10,
		SyncSubscriberTimeout:       3 * time.Second,
	}, primaryDB, nil, nil)
	if err != nil {
		t.Fatalf("modeltest: wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	mod := loader.LoadModule(ctx, rt, wasm.PoolConfig{
		WarmSize: 1, MaxSize: 4, BorrowTimeout: 10 * time.Second,
	}, loader.Source{Name: moduleSrcName, ManifestBytes: manifestBytes, WasmBytes: wasmBytes})
	if mod.Status == module.StatusFailed {
		t.Fatalf("modeltest: module failed to load: %s", mod.FailureReason)
	}
	// Registered after rt's own Close cleanup (line above) so it runs
	// first — t.Cleanup is LIFO. mod.Pool's replenishLoop goroutine can
	// still be mid-InstantiateModule when rt.Close fires otherwise,
	// racing under -race (the same pattern moduleinstall/worker_test.go
	// and modulereload/leader_test.go hit and fixed the same way).
	t.Cleanup(func() {
		mod.Pool.DrainAndClose(context.Background(), 5*time.Second)
		_ = mod.CompiledModule.Close(context.Background())
	})
	moduleName := mod.Manifest.Name

	tenantID := uuid.NewString()
	tenantSlug := randomTenantSlug()
	createTenantSchema(t, primaryDB, tenantSlug)

	syncModuleSchema(t, ctx, tenantID, tenantSlug, mod)
	mod.Status = module.StatusReady

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{moduleName: mod}); err != nil {
		t.Fatalf("modeltest: registry.Update: %v", err)
	}
	snap := reg.Snapshot()
	permReg := snap.PermissionRegistry()

	var allPerms permission.PermissionBitfield
	for _, name := range permReg.Names() {
		if idx, ok := permReg.Index(name); ok {
			allPerms.Set(idx)
		}
	}

	userID := cfg.userID
	if userID == "" {
		userID = uuid.NewString()
	}
	userPerms := allPerms
	if cfg.userID != "" {
		userPerms = permissionsToBitfield(permReg, cfg.userPerms)
	}

	h := &Harness{
		t:          t,
		TenantID:   tenantID,
		UserID:     userID,
		tenantSlug: tenantSlug,
		handler:    engine.NewModuleTestHandler(reg, rt),
		permReg:    permReg,
		moduleName: moduleName,
		allPerms:   userPerms,
	}
	h.DB = newTestDB(t, primaryDB, tenantID, tenantSlug)
	h.Events = newTestEvents(t, primaryDB, tenantID)

	t.Cleanup(func() { dropTenantSchema(t, primaryDB, tenantSlug) })

	for _, path := range cfg.fixturePaths {
		h.DB.SeedFromFixture(path)
	}

	return h
}

func testDBDSN() string {
	if dsn := os.Getenv("GOERP_TEST_DB_DSN"); dsn != "" {
		return dsn
	}
	return defaultTestDBDSN
}

func openTestPrimaryDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testDBDSN()
	conn, err := db.New(dsn)
	if err != nil {
		t.Skipf("modeltest: postgres not reachable at %s (start compose.dev.yml, or set GOERP_TEST_DB_DSN): %v", dsn, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	riverMigrateOnce.Do(func() {
		ctx := context.Background()
		pool, err := db.NewPgxPool(ctx, dsn)
		if err != nil {
			t.Fatalf("modeltest: open pgx pool for river migration: %v", err)
			return
		}
		defer pool.Close()
		if err := jobqueue.Migrate(ctx, pool); err != nil {
			t.Fatalf("modeltest: migrate river tables: %v", err)
		}
	})

	return conn
}

func sharedCompilationCacheDir() string {
	dir := os.Getenv("GOERP_TEST_WASM_CACHE")
	if dir == "" {
		dir = os.TempDir() + "/goerp-modeltest-wasm-cache"
	}
	return dir
}

// compileModuleUnderTest builds moduleDir/cmd/module to wasip1 WASM via
// the same internal/module.BuildWasm the `goerp module build` CLI uses,
// then patches the freshly-computed checksum into moduleDir/manifest.json's
// bytes in memory — the on-disk manifest's own checksum field reflects
// whatever a prior `goerp module build`/packaging step last computed, not
// this from-source recompile, so it can't be trusted as-is.
func compileModuleUnderTest(t *testing.T, ctx context.Context, moduleDir string) (wasmBytes, manifestBytes []byte, moduleName string) {
	t.Helper()

	result, err := internalmodule.BuildWasm(ctx, moduleDir, true)
	if err != nil {
		t.Fatalf("modeltest: compile module: %v", err)
	}
	if result == nil {
		t.Fatalf("modeltest: manifest.json declares wasm: false — modeltest requires a WASM module")
	}

	wasmBytes, err = os.ReadFile(result.WasmPath)
	if err != nil {
		t.Fatalf("modeltest: read compiled module: %v", err)
	}

	raw, err := os.ReadFile(moduleDir + "/manifest.json")
	if err != nil {
		t.Fatalf("modeltest: read manifest.json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("modeltest: parse manifest.json: %v", err)
	}
	decoded["checksum"] = result.WasmSHA256
	moduleName, _ = decoded["name"].(string)
	manifestBytes, err = json.Marshal(decoded)
	if err != nil {
		t.Fatalf("modeltest: re-marshal manifest.json: %v", err)
	}
	return wasmBytes, manifestBytes, moduleName
}

func createTenantSchema(t *testing.T, primaryDB *sql.DB, slug string) {
	t.Helper()
	if _, err := primaryDB.Exec(fmt.Sprintf(`CREATE SCHEMA %s`, tenantschema.Name(slug))); err != nil {
		t.Fatalf("modeltest: create tenant schema: %v", err)
	}
}

func dropTenantSchema(t *testing.T, primaryDB *sql.DB, slug string) {
	t.Helper()
	if _, err := primaryDB.Exec(fmt.Sprintf(`DROP SCHEMA %s CASCADE`, tenantschema.Name(slug))); err != nil {
		t.Logf("modeltest: drop tenant schema %s: %v", slug, err)
	}
}

// syncModuleSchema runs the same schema-sync pipeline moduleinstall.Worker
// runs in production (schema.SchemaDiffEngine.Diff/ExecuteAccepted, RLS
// policy sync, etag trigger sync) against the harness's fresh tenant
// schema, which starts with no tables at all — so every declared model
// diffs as a create.
//
// Uses its own short-lived *sql.DB rather than the harness's shared
// primaryDB: schema.SchemaSyncSession.BeginSync runs `SET search_path`
// on a checked-out connection and never resets it before returning that
// connection to its pool on Close — fine for a pool schema sync owns
// exclusively, but it would otherwise silently poison whichever
// unrelated query (h.DB, h.Events) next drew the same pooled connection
// from primaryDB.
func syncModuleSchema(t *testing.T, ctx context.Context, tenantID, tenantSlug string, mod *module.LoadedModule) {
	t.Helper()

	syncDB, err := db.New(testDBDSN())
	if err != nil {
		t.Fatalf("modeltest: open schema-sync connection: %v", err)
	}
	defer func() { _ = syncDB.Close() }()

	pool := schema.NewPool(syncDB, lockAcquireTimeout)
	sess, err := pool.BeginSync(ctx, tenantID, tenantSlug, mod.Manifest.Name, &mod.Manifest)
	if err != nil {
		t.Fatalf("modeltest: begin schema sync: %v", err)
	}
	defer func() { _ = sess.Close(ctx) }()

	diffEngine := schema.NewSchemaDiffEngine(&schema.Config{DDLStatementTimeout: 30 * time.Second})

	changes, err := diffEngine.Diff(ctx, sess, mod.ModelDecls, mod.TypeDecls)
	if err != nil {
		t.Fatalf("modeltest: diff schema: %v", err)
	}
	if _, _, err := diffEngine.ExecuteAccepted(ctx, sess, mod.ModelDecls, changes, nil); err != nil {
		t.Fatalf("modeltest: apply schema: %v", err)
	}
	if err := diffEngine.SyncRLSPolicies(ctx, sess, mod.ModelDecls, mod.Manifest.Policies); err != nil {
		t.Fatalf("modeltest: sync RLS policies: %v", err)
	}
	if len(mod.Manifest.AuditedTables) > 0 {
		if err := diffEngine.SyncEtagTriggers(ctx, sess, mod.ModelDecls, mod.Manifest.AuditedTables); err != nil {
			t.Fatalf("modeltest: sync etag triggers: %v", err)
		}
	}
	if err := sess.RecordSyncSuccess(ctx); err != nil {
		t.Fatalf("modeltest: record sync success: %v", err)
	}
}

func permissionsToBitfield(reg *permission.PermissionRegistry, names []string) permission.PermissionBitfield {
	var b permission.PermissionBitfield
	for _, name := range names {
		if idx, ok := reg.Index(name); ok {
			b.Set(idx)
		}
	}
	return b
}

func randomTenantSlug() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 12)
	for i := range buf {
		buf[i] = alphabet[rand.IntN(len(alphabet))]
	}
	return "test" + string(buf)
}
