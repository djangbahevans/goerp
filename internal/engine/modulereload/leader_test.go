package modulereload

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/moduleboot"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/workflowworker"
	"github.com/google/uuid"
)

// localPostgresDSN matches internal/engine/moduleinstall's own test
// convention — the compose.dev.yml Postgres instance.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// compileFixture compiles testdata/reloadfixture to wasip1 WASM. wide
// selects the two-column model variant (see reloadfixture's own doc
// comment on the wide build var) — reload tests always pass "" for a
// same-shape upgrade and only pass "1" for the downgrade-precheck test.
func compileFixture(t *testing.T, wide string) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "reloadfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared",
		"-ldflags", "-X main.wide="+wide,
		"-o", wasmPath, "./testdata/reloadfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/reloadfixture (wide=%q): %v\n%s", wide, err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// buildSource zips a minimal valid manifest.json (checksum matching
// wasmBytes) together with wasmBytes and parses it back out the same way
// moduleboot.DiscoverOne/ParsePackage do, returning the loader.Source +
// manifest.Manifest pair Leader.Run itself takes.
func buildSource(t *testing.T, name, version string, wasmBytes []byte, extra map[string]any) (loader.Source, manifest.Manifest) {
	t.Helper()

	sum := sha256.Sum256(wasmBytes)
	fields := map[string]any{
		"name":         name,
		"display_name": name,
		"type":         "domain",
		"version":      version,
		"description":  "a hot reload test module",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   []string{},
		"capabilities": []string{"db.read", "db.write"},
		"schema": map[string]any{
			"owned_models": []string{"widgets.widget"},
		},
		"checksum": fmt.Sprintf("sha256:%x", sum),
	}
	maps.Copy(fields, extra)

	manifestBytes, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeEntry := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	writeEntry("manifest.json", manifestBytes)
	writeEntry("module.wasm", wasmBytes)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	src, mf, err := moduleboot.ParsePackage(buf.Bytes())
	if err != nil {
		t.Fatalf("ParsePackage: %v", err)
	}
	return *src, *mf
}

type testEnv struct {
	conn        *sql.DB
	pool        *schema.SchemaSyncPool
	diffEngine  *schema.SchemaDiffEngine
	tenantStore *tenant.Store
	roleStore   *role.Store
	rt          *wasm.Runtime
}

// sharedTestCompilationCacheDir is one wazero compilation cache shared
// by every test in this package's own process, instead of a fresh
// t.TempDir() per test — the fixture WASM modules these tests load are
// the same handful across this package's own test functions, so a
// per-test cache bought nothing but redundant AOT compilation
// (goerp#527). Not cleaned up on its own; relies on the OS temp
// directory's own lifecycle, the same as any other os.MkdirTemp caller
// that isn't tied to a single test's own t.TempDir().
var sharedTestCompilationCacheDir = sync.OnceValue(func() string {
	dir, err := os.MkdirTemp("", "modulereload-test-cache-")
	if err != nil {
		panic(err)
	}
	return dir
})

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

	rt, err := wasm.New(&config.Config{
		CompilationCache:  sharedTestCompilationCacheDir(),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	return &testEnv{
		conn:        conn,
		pool:        pool,
		diffEngine:  schema.NewSchemaDiffEngine(&schema.Config{}),
		tenantStore: tenantStore,
		roleStore:   role.NewStore(conn),
		rt:          rt,
	}
}

func (e *testEnv) activeTenant(t *testing.T, slug string) tenant.Tenant {
	t.Helper()

	tt, err := e.tenantStore.CreateTenant(context.Background(), slug, "Test Tenant "+slug)
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
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

// uniqueSlug is a UUID-derived slug, not a raw time.Now().UnixNano() one —
// this package's own tests and internal/engine/moduleinstall's run as
// separate concurrent processes against the same shared dev Postgres
// (review-goerp's own documented convention), and a nanosecond timestamp
// can collide across processes under heavy scheduling contention. Only the
// first 12 hex characters — see moduleinstall's own uniqueSlug for why
// (some callers there concatenate two slugs into one manifest Name, capped
// at 64 characters) — this package doesn't need that budget itself, but
// matching the same short form keeps the two packages' test output
// directly comparable.
func uniqueSlug(t *testing.T) string {
	t.Helper()
	return "s" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
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

func columnExists(t *testing.T, conn *sql.DB, schemaName, table, column string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 AND column_name = $3)",
		schemaName, table, column,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("columnExists query: %v", err)
	}
	return exists
}

// newLeader builds a Leader wired against env, with a fresh, empty
// registry unless preloaded overrides it, and a local-disk storage.Backend
// so object-storage publish is real rather than mocked.
func newLeader(t *testing.T, env *testEnv, preloaded map[string]*module.LoadedModule) (*Leader, *registry.ModuleRegistry) {
	t.Helper()

	reg := &registry.ModuleRegistry{}
	if preloaded != nil {
		_, _ = reg.Update(preloaded)
	} else {
		_, _ = reg.Update(map[string]*module.LoadedModule{})
	}

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", filepath.Join(t.TempDir(), "objectstore"))
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New(local): %v", err)
	}

	cacheClient, err := cache.New(context.Background(), cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	// Drains and closes every module still live in reg at test end, before
	// newTestEnv's own t.Cleanup closes the shared env.rt (t.Cleanup runs
	// LIFO, and every test calls newTestEnv before newLeader, so this
	// always fires first). A test that calls Run more than once — most of
	// this file's own sequential-reload tests — otherwise leaves the final
	// reload's own pool live and un-drained: its replenishLoop goroutine
	// can still be mid-InstantiateModule when env.rt.Close() runs, a real
	// data race under -race (caught reproducing goerp#467's own CI
	// failure). Run's own async drain of a *superseded* pool (the one a
	// later reload replaced) is a separate, harder-to-observe race this
	// doesn't close — see the old-pool assertions below for how those
	// tests wait for it instead.
	t.Cleanup(func() {
		snap := reg.Snapshot()
		if snap == nil {
			return
		}
		for _, m := range snap.Modules() {
			m.Pool.DrainAndClose(context.Background(), 5*time.Second)
			_ = m.CompiledModule.Close(context.Background())
		}
	})

	return &Leader{
		Runtime:     env.rt,
		PoolCfg:     wasm.PoolConfig{MaxSize: 1, WarmSize: 0, BorrowTimeout: time.Second},
		Registry:    reg,
		RolePerms:   permcache.NewRolePermissionMap(),
		TenantStore: env.tenantStore,
		RoleStore:   env.roleStore,
		SyncPool:    env.pool,
		DiffEngine:  env.diffEngine,
		Storage:     backend,
		Cache:       cacheClient,
		Workers:     workflowworker.NewManager(nil, nil, ""),
	}, reg
}

func TestLeader_Run_FreshReloadSucceeds(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	src, mf := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)

	l, reg := newLeader(t, env, nil)
	if err := l.Run(context.Background(), name, src, mf); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	snap := reg.Snapshot()
	m, ok := snap.Modules()[name]
	if !ok {
		t.Fatalf("module %q not present in registry after reload", name)
	}
	if m.Status != module.StatusReady {
		t.Errorf("Status = %v, want StatusReady", m.Status)
	}
	if !tableExists(t, env.conn, "tenant_"+slug, "widgets_widget") {
		t.Error("expected the widget table to have been created")
	}
}

// TestLeader_Run_NilStorageFailsCleanly guards against the recurring
// nil-panic pattern this codebase has already hit three times for other
// warn-only-constructed dependencies (replica Postgres, Meilisearch,
// object storage — engine-internals.md §2): Storage is the same possibly-
// nil storage.Backend Engine.New leaves nil after a warn-only object
// storage connect failure, so Run must fail with a clear error rather than
// a nil-pointer panic when it's unset.
func TestLeader_Run_NilStorageFailsCleanly(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	src, mf := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)

	l, _ := newLeader(t, env, nil)
	l.Storage = nil

	err := l.Run(context.Background(), name, src, mf)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if err.Error() != "object storage unavailable" {
		t.Errorf("error = %q, want %q", err.Error(), "object storage unavailable")
	}
}

func TestLeader_Run_UpgradeSyncsNewColumnAndDrainsOldPool(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	l, reg := newLeader(t, env, nil)

	src1, mf1 := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)
	if err := l.Run(context.Background(), name, src1, mf1); err != nil {
		t.Fatalf("Run() v1 error: %v", err)
	}
	oldMod := reg.Snapshot().Modules()[name]

	src2, mf2 := buildSource(t, name, "1.1.0", compileFixture(t, "1"), nil)
	if err := l.Run(context.Background(), name, src2, mf2); err != nil {
		t.Fatalf("Run() v2 error: %v", err)
	}

	newMod := reg.Snapshot().Modules()[name]
	if newMod.Manifest.Version != "1.1.0" {
		t.Errorf("live version = %q, want 1.1.0", newMod.Manifest.Version)
	}
	if newMod == oldMod {
		t.Error("registry entry did not change identity across reload")
	}
	if !columnExists(t, env.conn, "tenant_"+slug, "widgets_widget", "extra") {
		t.Error("expected schema sync to add the new required column")
	}

	// The old pool is drained asynchronously (Run's own doc comment) — poll
	// briefly for Borrow to start reporting ErrPoolDraining rather than
	// asserting immediately. draining is set synchronously at the very top
	// of DrainAndClose, before any of its own work — Borrow reporting it
	// only proves that goroutine has started, not that its (fast, in-memory
	// only) close work has finished, so the short grace sleep afterward
	// gives it room to actually finish before this test function returns
	// and env.rt.Close() (via newTestEnv's own t.Cleanup) runs — same class
	// of race newLeader's own cleanup closes for the *final* pool, just
	// with no exported "fully closed" signal to poll for this superseded
	// one instead.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := oldMod.Pool.Borrow(context.Background())
		if errors.Is(err, wasm.ErrPoolDraining) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old pool was not draining within the deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(250 * time.Millisecond)
}

func TestLeader_Run_DowngradeWithIncompatibleColumnBlocked(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	l, reg := newLeader(t, env, nil)

	// Install the wide (two-column) variant first as 1.1.0 — "extra" is
	// nullable in the fixture, so schema sync adds it automatically as a
	// safe AddColumn.
	src1, mf1 := buildSource(t, name, "1.1.0", compileFixture(t, "1"), nil)
	if err := l.Run(context.Background(), name, src1, mf1); err != nil {
		t.Fatalf("Run() v1.1.0 error: %v", err)
	}
	if !columnExists(t, env.conn, "tenant_"+slug, "widgets_widget", "extra") {
		t.Fatal("expected the safe AddColumn to have landed before forcing it NOT NULL")
	}

	// Force the live column NOT NULL with no default — simulating a
	// separately-applied data migration (goerp#114/#292's own scope, not
	// this package's) having backfilled and locked it down. No existing
	// rows to violate the constraint, so this always succeeds regardless
	// of ordering against the assertion above.
	if _, err := env.conn.Exec(`ALTER TABLE ` + quoteIdent("tenant_"+slug) + `.widgets_widget ALTER COLUMN extra SET NOT NULL`); err != nil {
		t.Fatalf("force extra NOT NULL: %v", err)
	}

	// Reloading to the older, narrow (one-column) 1.0.0 is now a downgrade:
	// the live schema still has "extra", a NOT NULL column with no default
	// the older code never populates on INSERT — CheckDowngrade must block
	// it before any DDL runs.
	src2, mf2 := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)
	err := l.Run(context.Background(), name, src2, mf2)
	if err == nil {
		t.Fatal("expected an error for a blocked downgrade")
	}
	if !strings.Contains(err.Error(), "downgrade blocked") {
		t.Errorf("error = %q, want it to mention \"downgrade blocked\"", err.Error())
	}

	// The live registry entry must still be 1.1.0 — a blocked downgrade
	// must never publish.
	m := reg.Snapshot().Modules()[name]
	if m.Manifest.Version != "1.1.0" {
		t.Errorf("live version = %q, want it to still be 1.1.0 after a blocked downgrade", m.Manifest.Version)
	}
	if !columnExists(t, env.conn, "tenant_"+slug, "widgets_widget", "extra") {
		t.Error("blocked downgrade must not have dropped the live column")
	}
}

func TestLeader_Run_ConcurrentSameModuleReloads_OneSucceedsOneRejected(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	l, reg := newLeader(t, env, nil)

	src1, mf1 := buildSource(t, name, "1.0.0", compileFixture(t, ""), nil)
	if err := l.Run(context.Background(), name, src1, mf1); err != nil {
		t.Fatalf("Run() v1.0.0 error: %v", err)
	}

	src2, mf2 := buildSource(t, name, "1.1.0", compileFixture(t, "1"), nil)
	src3, mf3 := buildSource(t, name, "1.2.0", compileFixture(t, "1"), nil)

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0] = l.Run(context.Background(), name, src2, mf2) }()
	go func() { defer wg.Done(); results[1] = l.Run(context.Background(), name, src3, mf3) }()
	wg.Wait()

	successes, rejections := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, errReloadInProgress):
			rejections++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 || rejections != 1 {
		t.Errorf("results = %v, want exactly one success and one errReloadInProgress rejection", results)
	}

	m := reg.Snapshot().Modules()[name]
	if m.Manifest.Version != "1.1.0" && m.Manifest.Version != "1.2.0" {
		t.Errorf("live version = %q, want either candidate version", m.Manifest.Version)
	}
}
