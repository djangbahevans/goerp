package moduleinstall

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

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/workflowworker"
	"github.com/google/uuid"
)

// localPostgresDSN matches internal/engine/tenant/sync's own test
// convention — the compose.dev.yml Postgres instance.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// compileFixture compiles testdata/installfixture to wasip1 WASM, the
// same way `goerp module build` does — see that package's own doc
// comment (mirrors internal/engine/loader's compileRealFixture).
func compileFixture(t *testing.T) []byte {
	t.Helper()
	return compileFixtureVariant(t, "")
}

// compileFixtureVariant is compileFixture, but links a distinct variant
// string into the fixture's schema label via -ldflags -X — producing
// genuinely content-distinct WASM binaries, rather than the
// byte-for-byte identical output a deterministic Go build always
// produces from the same source. Needed wherever a test would otherwise
// have two differently-named "modules" share one entry in wasm.Runtime's
// content-addressed compilation cache (see Runtime.CompileModule's own
// doc comment) and so get skewed timing for a test that measures compile
// cost.
func compileFixtureVariant(t *testing.T, variant string) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "installfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared",
		"-ldflags", "-X main.variant="+variant,
		"-o", wasmPath, "./testdata/installfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/installfixture (variant=%q): %v\n%s", variant, err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// buildPackage zips a minimal valid manifest.json (checksum matching
// wasmBytes) together with wasmBytes into an in-memory .erp package —
// the same wire shape moduleboot.ParsePackage reads.
func buildPackage(t *testing.T, name string, wasmBytes []byte, extra map[string]any) []byte {
	t.Helper()

	sum := sha256.Sum256(wasmBytes)
	fields := map[string]any{
		"name":         name,
		"display_name": name,
		"type":         "domain",
		"version":      "1.0.0",
		"description":  "an install test module",
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
	return buf.Bytes()
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
	dir, err := os.MkdirTemp("", "moduleinstall-test-cache-")
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

// activeTenant creates a tenant, flips it to active, and creates its
// tenant_{slug} Postgres schema fresh — same preconditions
// tenant/sync's own tests assume, and cleaned up the same way (see that
// package's activeTenant for why module_schema_versions needs its own
// explicit cleanup).
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
// this package's own tests and internal/engine/modulereload's run as
// separate concurrent processes against the same shared dev Postgres
// (review-goerp's own documented convention), and a nanosecond timestamp
// has a real, if narrow, chance of colliding across processes under heavy
// scheduling contention. Only the first 12 hex characters (48 bits —
// collision probability is low enough to treat as never, even across many
// concurrent processes' worth of slugs): some callers build a module name
// out of two concatenated slugs (module + tenant), and manifest.Manifest's
// own Name validation caps at 64 characters — a full UUID would blow that
// budget.
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

// moduleSyncRecorded reports whether tenantID has a
// system.module_schema_versions row for moduleName — i.e. whether this
// exact module was ever synced against this exact tenant. Unlike
// tableExists, this is immune to a table another, unrelated concurrently
// running test's own module happens to also create in the same tenant
// schema (see TestWorker_Run_UnresolvableSubscriptionFailsBeforeTenantSync's
// own doc comment for why that's a real scenario, not a hypothetical one):
// the primary key is (tenant_id, module_name), so only this specific
// module's own sync could ever produce a row here.
func moduleSyncRecorded(t *testing.T, conn *sql.DB, tenantID, moduleName string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2)",
		tenantID, moduleName,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("moduleSyncRecorded query: %v", err)
	}
	return exists
}

// newWorker builds a Worker wired against env, with a fresh, empty
// registry unless preloaded overrides it.
func newWorker(t *testing.T, env *testEnv, preloaded map[string]*module.LoadedModule) (*Worker, *registry.ModuleRegistry) {
	t.Helper()

	reg := &registry.ModuleRegistry{}
	if preloaded != nil {
		_, _ = reg.Update(preloaded)
	} else {
		_, _ = reg.Update(map[string]*module.LoadedModule{})
	}

	// Drains and closes every module still live in reg at test end, before
	// newTestEnv's own t.Cleanup closes the shared env.rt (t.Cleanup runs
	// LIFO, and every test calls newTestEnv before newWorker, so this
	// always fires first). A test that installs more than one module —
	// several of this file's own tests, including the concurrent-install
	// ones — otherwise leaves at least one live pool un-drained: its
	// replenishLoop goroutine can still be mid-InstantiateModule when
	// env.rt.Close() runs, a real data race under -race (caught
	// reproducing goerp#467's own CI failure, on
	// TestWorker_Run_ConcurrentDifferentModules_OverlapCompileAndSync).
	t.Cleanup(func() {
		snap := reg.Snapshot()
		if snap == nil {
			return
		}
		for _, m := range snap.Modules() {
			if m.Pool == nil {
				continue
			}
			m.Pool.DrainAndClose(context.Background(), 5*time.Second)
			if m.CompiledModule != nil {
				_ = m.CompiledModule.Close(context.Background())
			}
		}
	})

	return &Worker{
		Runtime:     env.rt,
		PoolCfg:     wasm.PoolConfig{MaxSize: 1, WarmSize: 0, BorrowTimeout: time.Second},
		Registry:    reg,
		RolePerms:   permcache.NewRolePermissionMap(),
		TenantStore: env.tenantStore,
		RoleStore:   env.roleStore,
		SyncPool:    env.pool,
		DiffEngine:  env.diffEngine,
		Workers:     workflowworker.NewManager(nil, nil, ""),
	}, reg
}

func writeTempPackage(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.erp")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write package: %v", err)
	}
	return path
}

func TestWorker_Run_FreshInstallSucceeds(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	wasmBytes := compileFixture(t)
	name := "widgets_" + slug
	pkg := buildPackage(t, name, wasmBytes, nil)
	path := writeTempPackage(t, pkg)

	w, reg := newWorker(t, env, nil)
	result, err := w.run(context.Background(), Args{PackagePath: path})
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}

	if result.Module != name || result.Version != "1.0.0" {
		t.Errorf("Result = %+v, want Module=%q Version=1.0.0", result, name)
	}
	// Succeeded/Failed can also carry other tenants that happen to be
	// active in the shared dev database (leftover fixtures from other
	// packages' tests, e.g. schema/pool_test.go's) — assert this test's
	// own tenant landed in Succeeded rather than asserting exact slice
	// contents, the same containment check tenant/sync's own tests use
	// for the identical reason.
	found := false
	for _, s := range result.Succeeded {
		if s == slug {
			found = true
		}
	}
	if !found {
		t.Errorf("Succeeded = %+v, want it to contain %q", result.Succeeded, slug)
	}
	for _, r := range result.Failed {
		if r.Tenant == slug {
			t.Errorf("Failed unexpectedly contains this test's own tenant %q: %+v", slug, r)
		}
	}

	if !tableExists(t, env.conn, "tenant_"+slug, "widgets_widget") {
		t.Error("expected the widget table to have been created")
	}

	snap := reg.Snapshot()
	m, ok := snap.Modules()[name]
	if !ok {
		t.Fatalf("module %q not present in registry after install", name)
	}
	if m.Status != module.StatusReady {
		t.Errorf("Status = %v, want StatusReady", m.Status)
	}
}

func TestWorker_Run_AlreadyLoadedModuleRejected(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	existing := &module.LoadedModule{
		Status:   module.StatusReady,
		Manifest: manifest.Manifest{Name: name, Version: "0.9.0"},
	}
	w, _ := newWorker(t, env, map[string]*module.LoadedModule{name: existing})

	wasmBytes := compileFixture(t)
	pkg := buildPackage(t, name, wasmBytes, nil)
	path := writeTempPackage(t, pkg)

	_, err := w.run(context.Background(), Args{PackagePath: path})
	if err == nil {
		t.Fatal("expected an error installing an already-loaded module")
	}
	if !strings.Contains(err.Error(), "already loaded") {
		t.Errorf("error = %q, want it to mention \"already loaded\"", err.Error())
	}
}

func TestWorker_Run_PartialTenantFailureStillReachesReady(t *testing.T) {
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
	// Deliberately no tenant_{badSlug} schema — Diff fails against it.

	wasmBytes := compileFixture(t)
	name := "widgets_" + goodSlug + "_" + badSlug
	pkg := buildPackage(t, name, wasmBytes, nil)
	path := writeTempPackage(t, pkg)

	w, reg := newWorker(t, env, nil)
	result, err := w.run(context.Background(), Args{PackagePath: path})
	if err != nil {
		t.Fatalf("run() error: %v", err)
	}

	failedBad := false
	for _, r := range result.Failed {
		if r.Tenant == badSlug {
			failedBad = true
			if r.Error == "" {
				t.Error("expected the bad tenant's failure to carry a non-empty Error")
			}
		}
	}
	if !failedBad {
		t.Errorf("Failed = %+v, want it to contain tenant %q", result.Failed, badSlug)
	}

	snap := reg.Snapshot()
	m, ok := snap.Modules()[name]
	if !ok || m.Status != module.StatusReady {
		t.Errorf("module %q Status = %v (present=%v), want StatusReady despite one tenant failing", name, m, ok)
	}
}

// TestWorker_Run_ConcurrentDifferentModulesBothLandInRegistry guards the
// race Worker.mu's own doc comment describes: ModuleRegistry.Update
// replaces the entire module map on every call, so a naive
// read-snapshot/merge/Update sequence run by two installs at once can
// have the second overwrite the first's addition with a map built from a
// stale snapshot. mu now guards only publish's own merge/Update/RebuildAll
// sequence (goerp#487), not run's entire body, so this no longer proves
// its point by construction of a full serialization — but publish's own
// mu-guarded recheck still has to make exactly this true: two different
// modules installed concurrently must both still land in the registry, and
// a regression that lets one silently overwrite the other's entry would
// show up here as a lost update.
func TestWorker_Run_ConcurrentDifferentModulesBothLandInRegistry(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	// Distinct variants (see compileFixtureVariant's own doc comment) so
	// the two modules don't own the identical model/table name — both
	// installing into the one tenant above at the same time, they'd
	// otherwise race each other's CREATE TABLE for the same physical
	// table and could hit a genuine, unrelated Postgres error.
	nameA := "widgets_a_" + slug
	nameB := "widgets_b_" + slug
	pathA := writeTempPackage(t, buildPackage(t, nameA, compileFixtureVariant(t, "a_"+slug), nil))
	pathB := writeTempPackage(t, buildPackage(t, nameB, compileFixtureVariant(t, "b_"+slug), nil))

	w, reg := newWorker(t, env, nil)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = w.run(context.Background(), Args{PackagePath: pathA})
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = w.run(context.Background(), Args{PackagePath: pathB})
	}()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("run() [%d] error: %v", i, err)
		}
	}

	snap := reg.Snapshot()
	modules := snap.Modules()
	if _, ok := modules[nameA]; !ok {
		t.Errorf("module %q missing from registry after concurrent install (lost update)", nameA)
	}
	if _, ok := modules[nameB]; !ok {
		t.Errorf("module %q missing from registry after concurrent install (lost update)", nameB)
	}
}

// TestWorker_Run_ConcurrentDifferentModules_OverlapCompileAndSync is a
// timing-based regression guard for goerp#487: mu now guards only
// publish's final registry-merge step, not run's entire body, so two
// different modules' compile (loader.LoadModule) and tenant-sync
// (tenantsync.SyncModule) phases should run fully concurrently instead of
// fully serializing behind each other. A regression back to holding mu
// for run's entire body would make two concurrent installs take roughly
// 2x a single install's own duration; this asserts they finish well
// under that.
func TestWorker_Run_ConcurrentDifferentModules_OverlapCompileAndSync(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	// Each install gets its own variant, compiled to genuinely distinct
	// WASM content (see compileFixtureVariant's own doc comment) — so
	// every one of the three below pays a real, comparable compile cost
	// instead of two of them hitting a warm wazero compilation cache
	// because they happen to share identical bytes with the baseline.
	baselineName := "widgets_baseline_" + slug
	baselinePath := writeTempPackage(t, buildPackage(t, baselineName, compileFixtureVariant(t, "baseline_"+slug), nil))

	w, _ := newWorker(t, env, nil)

	baselineStart := time.Now()
	if _, err := w.run(context.Background(), Args{PackagePath: baselinePath}); err != nil {
		t.Fatalf("baseline run() error: %v", err)
	}
	baseline := time.Since(baselineStart)

	nameA := "widgets_overlap_a_" + slug
	nameB := "widgets_overlap_b_" + slug
	pathA := writeTempPackage(t, buildPackage(t, nameA, compileFixtureVariant(t, "a_"+slug), nil))
	pathB := writeTempPackage(t, buildPackage(t, nameB, compileFixtureVariant(t, "b_"+slug), nil))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	concurrentStart := time.Now()
	go func() {
		defer wg.Done()
		_, errs[0] = w.run(context.Background(), Args{PackagePath: pathA})
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = w.run(context.Background(), Args{PackagePath: pathB})
	}()
	wg.Wait()
	concurrent := time.Since(concurrentStart)

	for i, err := range errs {
		if err != nil {
			t.Fatalf("run() [%d] error: %v", i, err)
		}
	}

	// Fully serialized (the pre-#487 behavior) would take roughly
	// 2*baseline; a generous 1.7x threshold stays comfortably under that
	// while tolerating scheduler/CI noise around genuinely concurrent
	// (~1x baseline) runs.
	if threshold := baseline + (baseline * 7 / 10); concurrent > threshold {
		t.Errorf("two concurrent installs of different modules took %s (single-install baseline %s) — want well under 2x baseline (threshold %s); looks serialized", concurrent, baseline, threshold)
	}
}

// TestWorker_Run_ConcurrentSameNameInstalls_OneSucceedsOneRejected guards
// the race a lock scoped only to the registry-publish step would leave
// open: the "already loaded" check and the compile/sync/publish that
// follows it are not atomic on their own, so two concurrent installs of
// the same new module name could both pass the check, both fully load and
// sync independently, and whichever published second would silently
// discard the other's already-live module — leaking its pool, since
// nothing else ever reaches an unpublished LoadedModule to close it. The
// loser can be rejected at either of two points: reserve's fast-fail
// (errInstallInProgress, if it loses the race to claim the name before
// starting its own compile/sync), or publish's own recheck
// (errAlreadyLoaded, if it already finished its own compile/sync before
// losing there) — either way, exactly one install succeeds and the loser
// never leaks a pool.
func TestWorker_Run_ConcurrentSameNameInstalls_OneSucceedsOneRejected(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	wasmBytes := compileFixture(t)
	name := "widgets_" + slug
	// Two independently-written copies of the identical package content —
	// same name/version — so whichever run() acquires mu second sees the
	// exact module the first one just published.
	path1 := writeTempPackage(t, buildPackage(t, name, wasmBytes, nil))
	path2 := writeTempPackage(t, buildPackage(t, name, wasmBytes, nil))

	w, reg := newWorker(t, env, nil)

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, results[0] = w.run(context.Background(), Args{PackagePath: path1})
	}()
	go func() {
		defer wg.Done()
		_, results[1] = w.run(context.Background(), Args{PackagePath: path2})
	}()
	wg.Wait()

	succeeded, rejected := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, errAlreadyLoaded), errors.Is(err, errInstallInProgress):
			rejected++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Errorf("results = %v, want exactly one success and one rejection (errAlreadyLoaded or errInstallInProgress)", results)
	}

	snap := reg.Snapshot()
	if _, ok := snap.Modules()[name]; !ok {
		t.Errorf("module %q missing from registry after the winning install", name)
	}
}

// TestWorker_Run_UnresolvableSubscriptionFailsBeforeTenantSync exercises
// two things together: the defer in run that closes m.Pool/m.
// CompiledModule when a step after loader.LoadModule fails (LoadModule's
// own internal cleanup only covers a failure inside itself, per its doc
// comment, so anything failing after a successful LoadModule is this
// function's own responsibility to close, or it leaks the pool's
// replenish goroutine and warmed instances for the life of the process),
// and that validateNewModuleSubscriptions runs — and fails the module —
// before tenantsync.SyncModule ever touches a tenant's database.
// Ordering it this way matters: schema sync is additive-only DDL with no
// rollback path, so a module that's going to be rejected anyway must
// never get the chance to apply real schema changes to every active
// tenant first. Subscribing to an event nothing emits, with no
// soft_depends_on to excuse it, is what triggers the failure. This can't
// directly assert the pool was closed (DrainAndClose/Close are
// unexported implementation details with no query surface), but does
// confirm: the module never reaches the registry, this module was never
// recorded as synced against the tenant (proving sync never ran for it),
// and the persisted package file is removed rather than left for a future
// engine restart to rediscover and fail identically forever.
//
// Checking system.module_schema_versions for this tenant+module pair,
// rather than checking whether the widgets_widget table exists in the
// tenant's schema: ActiveTenants() (tenantsync's own enumeration) has no
// per-test or per-process scoping — every module-install/reload test
// suite in this repo runs against the one shared dev Postgres, so the
// moment this test's own tenant goes active, any other concurrently
// running test's own (unrelated, correctly-behaving) install can pick it
// up as one of "its" active tenants and sync its own module into it. Since
// most of this repo's fixtures default to an unqualified "widgets.widget"
// model (same physical table name, different owning module), a bare
// table-existence check can observe a real table an entirely different
// test legitimately created — not evidence this test's own sync ran.
// module_schema_versions is keyed (tenant_id, module_name), so it only
// ever reflects what this test's own uniquely-named module did, immune to
// that cross-test/cross-process interference.
func TestWorker_Run_UnresolvableSubscriptionFailsBeforeTenantSync(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)

	wasmBytes := compileFixture(t)
	name := "widgets_" + slug
	pkg := buildPackage(t, name, wasmBytes, map[string]any{
		"subscribes": []map[string]any{{"name": "nothing.emits.this"}},
	})
	path := writeTempPackage(t, pkg)

	w, reg := newWorker(t, env, nil)
	_, err := w.run(context.Background(), Args{PackagePath: path})
	if err == nil {
		t.Fatal("expected an error from an unresolvable event subscription")
	}
	if !strings.Contains(err.Error(), "validate event subscriptions") {
		t.Errorf("error = %q, want it to mention event subscription validation", err.Error())
	}

	snap := reg.Snapshot()
	if _, ok := snap.Modules()[name]; ok {
		t.Errorf("module %q should not be present in the registry after a validation failure", name)
	}
	if moduleSyncRecorded(t, env.conn, tt.ID, name) {
		t.Error("expected no module_schema_versions row for this module — validation should fail before tenant sync ever runs")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("expected the persisted package at %q to be removed after a permanent failure, stat error = %v", path, statErr)
	}
}

// TestWorker_Run_AlreadyLoadedRejection_DoesNotRemovePackageFile guards
// installer.go's own documented reasoning for excluding this one
// rejection from run's cleanup: the "already loaded" path is reached
// only when a module of this name is already live, and the package file
// at a deterministic "{name}-{version}.erp" path may be that module's
// own backing file — removing it here would delete a currently-loaded,
// healthy module's file out from under it, breaking that module on the
// next engine restart's moduleboot.Discover pass.
func TestWorker_Run_AlreadyLoadedRejection_DoesNotRemovePackageFile(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)

	name := "widgets_" + slug
	existing := &module.LoadedModule{
		Status:   module.StatusReady,
		Manifest: manifest.Manifest{Name: name, Version: "0.9.0"},
	}
	w, _ := newWorker(t, env, map[string]*module.LoadedModule{name: existing})

	wasmBytes := compileFixture(t)
	pkg := buildPackage(t, name, wasmBytes, nil)
	path := writeTempPackage(t, pkg)

	_, err := w.run(context.Background(), Args{PackagePath: path})
	if err == nil || !strings.Contains(err.Error(), "already loaded") {
		t.Fatalf("run() error = %v, want an \"already loaded\" rejection", err)
	}

	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("expected the package file at %q to still exist after an \"already loaded\" rejection, stat error = %v", path, statErr)
	}
}

// TestWorker_Publish_RegistryUpdateSucceedsDespiteRebuildAllFailure guards
// the distinction publish's committed return value exists for:
// Registry.Update (which touches no database) can succeed — making m
// live and routable — even when the RolePerms.RebuildAll call right
// after it fails (e.g. a transient DB error enumerating active tenants).
// run's cleanup defer must treat that as committed, not as "never
// published," or it would close the pool of a module the registry
// snapshot now actually points to.
func TestWorker_Publish_RegistryUpdateSucceedsDespiteRebuildAllFailure(t *testing.T) {
	env := newTestEnv(t)
	w, reg := newWorker(t, env, nil)

	// Closing the connection makes RolePerms.RebuildAll's own
	// ActiveTenants call fail without touching Registry.Update at all —
	// that call never reaches the database.
	if err := env.conn.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}

	m := &module.LoadedModule{Status: module.StatusReady, Manifest: manifest.Manifest{Name: "publish_commit_test", Version: "1.0.0"}}
	committed, err := w.publish(context.Background(), m)

	if !committed {
		t.Error("committed = false, want true — Registry.Update itself should have succeeded")
	}
	if err == nil {
		t.Fatal("expected an error from RebuildAll against a closed connection")
	}

	snap := reg.Snapshot()
	if _, ok := snap.Modules()[m.Manifest.Name]; !ok {
		t.Error("module missing from registry despite committed=true")
	}
}

// TestWorker_Run_InstallInProgressRejection_RemovesPackageFile guards the
// distinction run's cleanup defer draws between its two "already
// unavailable" rejections: errAlreadyLoaded (this exact name/version may
// be the currently-loaded module's own backing file — never removed) and
// errInstallInProgress (this name was never actually loaded; the losing
// install's package file backs nothing and must still be removed, or it
// leaks in ModuleDir exactly like any other permanent failure would).
// Reserves the name directly rather than racing two goroutines, so the
// "in progress" branch is hit deterministically instead of depending on
// which goroutine's reserve() call happens to run first.
func TestWorker_Run_InstallInProgressRejection_RemovesPackageFile(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)

	name := "widgets_" + slug
	w, _ := newWorker(t, env, nil)

	release, err := w.reserve(name)
	if err != nil {
		t.Fatalf("reserve() error: %v", err)
	}
	t.Cleanup(release)

	wasmBytes := compileFixture(t)
	pkg := buildPackage(t, name, wasmBytes, nil)
	path := writeTempPackage(t, pkg)

	_, err = w.run(context.Background(), Args{PackagePath: path})
	if err == nil {
		t.Fatal("expected an error installing a name that's already reserved")
	}
	if !errors.Is(err, errInstallInProgress) {
		t.Errorf("error = %v, want errInstallInProgress", err)
	}
	if errors.Is(err, errAlreadyLoaded) {
		t.Errorf("error = %v, should not also be errAlreadyLoaded", err)
	}

	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("expected the persisted package at %q to be removed, stat error = %v", path, statErr)
	}
}
