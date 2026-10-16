// Package modeltest is the module test harness: it compiles the module
// under test to wasip1 WASM, loads it into a real Wazero runtime, syncs its
// declared schema into a fresh Postgres tenant schema, and lets a test make
// HTTP-shaped requests against its routes and assert on the response, on
// emitted events, queued jobs and database state. It also delivers events
// and runs jobs synchronously through the module's WASM exports, with typed
// tenant config fixtures. It runs against real Postgres
// (GOERP_TEST_DB_DSN) and Redis, with no mocks.
//
// Import only from `_test.go` files, in the module's own root package
// (the directory containing manifest.json and cmd/module).
package modeltest

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/v2"
	"math/rand/v2"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/eventdelivery"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	internalmodule "github.com/djangbahevans/goerp/internal/module"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

// River migration is shared across harnesses to avoid repeated database round trips.
var riverMigrateOnce sync.Once

const defaultTestDBDSN = "postgres://goerp:dev@localhost:15432/goerp"

const lockAcquireTimeout = 10 * time.Second

// Harness runs module tests in an isolated tenant and is cleaned up when the test finishes.
type Harness struct {
	t *testing.T

	TenantID string
	UserID   string

	Model         *TestModel
	DB            *TestDB
	Events        *TestEvents
	Jobs          *TestJobs
	Config        *TestConfig
	Cache         *TestCache
	Notifications *TestNotifications
	Storage       *TestStorage

	tenantSlug string
	handler    http.Handler
	permReg    *permission.PermissionRegistry
	moduleName string
	allPerms   permission.PermissionBitfield
	runtime    *wasm.Runtime
	registry   *registry.ModuleRegistry
	module     *module.LoadedModule
	tenants    *tenant.Store
	roles      *role.Store
	eventTimes sync.Map
}

// Option configures a Harness at construction time.
type Option func(*harnessConfig)

type harnessConfig struct {
	locale       string
	clock        func() time.Time
	userID       string
	userPerms    []perm.Permission
	fixturePaths []string
	configValues []func(*Harness)
}

// WithLocale overrides the resolved locale for every harness invocation.
// Timezone and currency retain their resolved values.
func WithLocale(locale string) Option {
	return func(c *harnessConfig) { c.locale = locale }
}

// WithClock sets initialization and request wall time. Each request sees one fixed value.
// The function must be safe for concurrent calls; elapsed measurements and sleeps stay real.
func WithClock(now func() time.Time) Option {
	return func(c *harnessConfig) { c.clock = now }
}

// WithUser makes id, with perms, the harness's default user instead of
// the ordinary all-permissions default.
func WithUser(id string, perms []perm.Permission) Option {
	return func(c *harnessConfig) {
		c.userID = id
		c.userPerms = perms
	}
}

// WithFixture seeds the harness from a JSON fixture file once the harness
// (and the module's schema) is ready. SeedFromFixture defines the file shape.
func WithFixture(path string) Option {
	return func(c *harnessConfig) { c.fixturePaths = append(c.fixturePaths, path) }
}

// NewHarness compiles the module under test (the current package's
// sibling cmd/module, per its own manifest.json) to wasip1 WASM, loads it
// into a real Wazero runtime, creates a fresh Postgres tenant schema, and
// syncs the module's declared schema into it. The harness and its tenant
// schema are dropped automatically via t.Cleanup.
//
// Requires reachable Postgres at GOERP_TEST_DB_DSN, defaulting to
// postgres://goerp:dev@localhost:15432/goerp. The test skips when it is unreachable.
func NewHarness(t *testing.T, opts ...Option) *Harness {
	t.Helper()
	ctx := t.Context()

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

	cacheClient := openTestRedis(t)

	storageBackend, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("modeltest: create local storage backend: %v", err)
	}

	rt, err := wasm.New(&config.Config{
		CompilationCache:            sharedCompilationCacheDir(),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           64 << 20,
		DBMaxConcurrentTransactions: 10,
		SyncSubscriberTimeout:       3 * time.Second,
		StorageMaxFileBytes:         100 << 20,
	}, primaryDB, storageBackend, cacheClient, wasm.WithClock(cfg.clock))
	if err != nil {
		t.Fatalf("modeltest: wasm.New: %v", err)
	}
	rt.SetSchemaSyncDB(primaryDB)
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	mod := loader.LoadModule(ctx, rt, wasm.PoolConfig{
		WarmSize:      1,
		MaxSize:       4,
		BorrowTimeout: 10 * time.Second,
	}, loader.Source{
		Name:                moduleSrcName,
		ManifestBytes:       manifestBytes,
		WasmBytes:           wasmBytes,
		BackendTranslations: readBackendTranslations(t, moduleDir),
	})
	if mod.Status == module.StatusFailed {
		t.Fatalf("modeltest: module failed to load: %s", mod.FailureReason)
	}
	// Pools must stop before the runtime closes, including background instantiation.
	t.Cleanup(func() {
		mod.Pool.DrainAndClose(context.Background(), 5*time.Second)
		_ = mod.CompiledModule.Close(context.Background())
	})
	moduleName := mod.Manifest.Name
	setTranslationLookup(rt, mod)

	tenantID := uuid.New().String()
	tenantSlug := randomTenantSlug()
	createTenantSchema(t, primaryDB, tenantSlug)

	syncModuleSchema(t, ctx, tenantID, tenantSlug, mod)
	mod.Status = module.StatusReady

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{moduleName: mod}); err != nil {
		t.Fatalf("modeltest: registry.Update: %v", err)
	}
	if mod.Status == module.StatusFailed {
		t.Fatalf("modeltest: module registry rejected module: %s", mod.FailureReason)
	}
	snap := reg.Snapshot()
	permReg := snap.PermissionRegistry()

	var allPerms permission.PermissionBitfield
	for _, name := range permReg.Names() {
		if idx, ok := permReg.Index(name); ok {
			allPerms.Set(idx)
		}
	}

	userID := cmp.Or(cfg.userID, uuid.New().String())
	userPerms := allPerms
	if cfg.userID != "" {
		userPerms = permissionsToBitfield(permReg, cfg.userPerms)
	}

	h := &Harness{
		t:          t,
		TenantID:   tenantID,
		UserID:     userID,
		tenantSlug: tenantSlug,
		handler:    engine.NewModuleTestHandler(reg, rt, primaryDB),
		permReg:    permReg,
		moduleName: moduleName,
		allPerms:   userPerms,
		runtime:    rt,
		registry:   reg,
		module:     mod,
	}
	h.Model = &TestModel{h: h}
	h.DB = newTestDB(t, primaryDB, tenantID, tenantSlug)
	h.Events = newTestEvents(t, primaryDB, tenantID)
	h.Jobs = newTestJobs(t, primaryDB, tenantID, moduleName)
	h.Storage = newTestStorage(t, ctx, primaryDB, storageBackend, tenantID, tenantSlug, userID)
	h.setupIdentity(primaryDB, cfg)
	rt.SetSyncEventDispatcher(&eventdelivery.SyncDispatcher{
		ModuleRegistry: reg,
		Invoker: &eventdelivery.HandlerInvoker{
			Runtime:     rt,
			TenantStore: h.tenants,
			Roles:       h.roles,
		},
	})
	h.Config = newTestConfig(h, primaryDB)
	h.Cache = newTestCache(t, cacheClient, tenantID, moduleName)
	h.Notifications = newTestNotifications(t, primaryDB, h, mod, reg, rt, moduleDir, cfg.clock)
	if cacheClient != nil {
		t.Cleanup(func() { _ = cacheClient.DeleteByPrefix(context.Background(), tenantID+":") })
	}

	for _, path := range cfg.fixturePaths {
		h.DB.SeedFromFixture(path)
	}

	for _, set := range cfg.configValues {
		set(h)
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
		ctx := t.Context()
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

// The packaged checksum must match the test build even when the manifest is stale.
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

	wasmtest.Precompile(t, wasmBytes, sharedCompilationCacheDir())

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
	manifestBytes, err = json.Marshal(decoded, json.Deterministic(true))
	if err != nil {
		t.Fatalf("modeltest: re-marshal manifest.json: %v", err)
	}
	return wasmBytes, manifestBytes, moduleName
}

func createTenantSchema(t *testing.T, primaryDB *sql.DB, slug string) {
	t.Helper()
	// Registered first, so a later setup failure still drops the schema.
	t.Cleanup(func() { dropTenantSchema(t, primaryDB, slug) })
	if err := tenantschema.Create(t.Context(), primaryDB, slug); err != nil {
		t.Fatalf("modeltest: create tenant schema (the database needs docker/postgres-initdb's roles and functions; recreate the dev stack with docker compose down -v): %v", err)
	}
	if err := enginetables.CreateAll(t.Context(), primaryDB, slug, nil); err != nil {
		t.Fatalf("modeltest: create engine-owned tenant tables: %v", err)
	}
}

func dropTenantSchema(t *testing.T, primaryDB *sql.DB, slug string) {
	t.Helper()
	if err := tenantschema.Drop(context.Background(), primaryDB, slug); err != nil {
		t.Logf("modeltest: drop tenant schema %s: %v", slug, err)
	}
}

// Schema sync retains search_path on pooled connections; a dedicated pool prevents
// that session state from affecting unrelated harness queries.
func syncModuleSchema(t *testing.T, ctx context.Context, tenantID, tenantSlug string, mod *module.LoadedModule) {
	t.Helper()

	syncDB, err := db.New(testDBDSN())
	if err != nil {
		t.Fatalf("modeltest: open schema-sync connection: %v", err)
	}
	defer func() { _ = syncDB.Close() }()

	pool := schema.NewPool(syncDB, lockAcquireTimeout)
	if err := pool.Bootstrap(ctx); err != nil {
		t.Fatalf("modeltest: bootstrap system schema: %v", err)
	}
	sess, err := pool.BeginSync(ctx, tenantID, tenantSlug, mod.Manifest.Name, &mod.Manifest)
	if err != nil {
		t.Fatalf("modeltest: begin schema sync: %v", err)
	}
	defer func() { _ = sess.Close(ctx) }()

	diffEngine := schema.NewSchemaDiffEngine(&schema.Config{DDLStatementTimeout: 30 * time.Second, TolerateUnloadedDependencies: true})

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
	if err := diffEngine.SyncTenantRoleGrants(ctx, sess, mod.ModelDecls); err != nil {
		t.Fatalf("modeltest: sync tenant role grants: %v", err)
	}
	if err := sess.RecordSyncSuccess(ctx); err != nil {
		t.Fatalf("modeltest: record sync success: %v", err)
	}
}

func permissionsToBitfield(reg *permission.PermissionRegistry, perms []perm.Permission) permission.PermissionBitfield {
	var b permission.PermissionBitfield
	for _, p := range perms {
		if idx, ok := reg.Index(p.Name()); ok {
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
