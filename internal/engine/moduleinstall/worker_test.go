package moduleinstall

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/internal/engine/workflowworker"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

// connectTenantConn dials a real WebSocket connection registered with hub
// under tenantID (mirroring dispatchWSRoute's own accept-and-serve, which
// lives in the engine package and can't be called from here), subscribes it
// to tenantID's TenantChannel, and returns the client side for reading
// broadcasts sent to that channel.
func connectTenantConn(t *testing.T, hub *ws.Hub, tenantID string) *websocket.Conn {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.Serve(r.Context(), conn, uuid.New().String(), "user-1", tenantID, "test-agent")
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Listener.Addr().String(), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	if err := wsjson.Write(ctx, conn, map[string]string{"type": "subscribe", "channel": ws.TenantChannel(tenantID)}); err != nil {
		t.Fatalf("subscribe write: %v", err)
	}
	return conn
}

// localPostgresDSN matches internal/engine/tenant/sync's own test
// convention — the compose.dev.yml Postgres instance.
const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

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
	return buildPackageWithMembers(t, name, wasmBytes, extra, nil)
}

// buildPackageWithMembers is buildPackage plus extra archive members, by
// path.
func buildPackageWithMembers(t *testing.T, name string, wasmBytes []byte, extra map[string]any, members map[string][]byte) []byte {
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
	for _, path := range slices.Sorted(maps.Keys(members)) {
		writeEntry(path, members[path])
	}
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

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	pool := schema.NewPool(conn, 5*time.Second)
	if err := pool.Bootstrap(t.Context()); err != nil {
		t.Fatalf("schema pool Bootstrap() error: %v", err)
	}

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(t.Context()); err != nil {
		t.Fatalf("tenant store Bootstrap() error: %v", err)
	}

	rt, err := wasm.New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
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

	tt, err := e.tenantStore.CreateTenant(t.Context(), slug, "Test Tenant "+slug)
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

	if err := notifications.NewStore(e.conn).BootstrapTemplates(t.Context(), slug); err != nil {
		t.Fatalf("bootstrap notification templates: %v", err)
	}

	return *tt
}

// Short UUID-derived slugs avoid cross-process timestamp collisions while leaving room for
// concatenated module names under the 64-character limit.
func uniqueSlug(t *testing.T) string {
	t.Helper()
	return "s" + strings.ReplaceAll(uuid.New().String(), "-", "")[:12]
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

// Check sync metadata by tenant/module because concurrent test modules can create the same
// physical table through unrelated syncs.
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

	// Drain live pools before the runtime closes; replenishment can otherwise race with
	// runtime shutdown. Cleanup runs in LIFO order.
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
	result, err := w.run(t.Context(), Args{PackagePath: path})
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

func TestWorker_Run_PublishesFrontendTranslationsForTheInstalledVersion(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	name := "widgets_" + slug
	en := []byte(`{"actions.create":"New widget"}`)
	fr := []byte(`{"actions.create":"Nouveau widget"}`)
	pkg := buildPackageWithMembers(t, name, compileFixture(t), nil, map[string][]byte{
		"frontend/translations/en.json": en,
		"frontend/translations/fr.json": fr,
	})

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", filepath.Join(t.TempDir(), "objectstore"))
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New(local): %v", err)
	}
	w, _ := newWorker(t, env, nil)
	w.Storage = backend
	if _, err := w.run(t.Context(), Args{PackagePath: writeTempPackage(t, pkg)}); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	for locale, want := range map[string][]byte{"en": en, "fr": fr} {
		key, err := module.LiveFrontendTranslationKey(t.Context(), backend, name, "1.0.0", locale)
		if err != nil || key == "" {
			t.Fatalf("live %s translations key = %q, error %v", locale, key, err)
		}
		rc, _, err := backend.Download(t.Context(), key)
		if err != nil {
			t.Fatalf("download %s translations: %v", locale, err)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		if !bytes.Equal(got, want) {
			t.Errorf("%s translations = %s, want %s", locale, got, want)
		}
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

	_, err := w.run(t.Context(), Args{PackagePath: path})
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
	badTenant, err := env.tenantStore.CreateTenant(t.Context(), badSlug, "No Schema Tenant")
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
	result, err := w.run(t.Context(), Args{PackagePath: path})
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

// Concurrent registry publications must merge under the same lock to avoid overwriting
// additions from stale snapshots.
func TestWorker_Run_ConcurrentDifferentModulesBothLandInRegistry(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	// Distinct physical model names prevent concurrent installs from racing to create one
	// table.
	nameA := "widgets_a_" + slug
	nameB := "widgets_b_" + slug
	pathA := writeTempPackage(t, buildPackage(t, nameA, compileFixtureVariant(t, "a_"+slug), nil))
	pathB := writeTempPackage(t, buildPackage(t, nameB, compileFixtureVariant(t, "b_"+slug), nil))

	w, reg := newWorker(t, env, nil)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Go(func() {
		_, errs[0] = w.run(t.Context(), Args{PackagePath: pathA})
	})
	wg.Go(func() {
		_, errs[1] = w.run(t.Context(), Args{PackagePath: pathB})
	})
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

// Concurrent installs should overlap compilation and schema sync; the publish lock must
// not serialize those slow phases.
func TestWorker_Run_ConcurrentDifferentModules_OverlapCompileAndSync(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	// Distinct WASM bytes force comparable compilation work instead of hitting a warm
	// cache.
	baselineName := "widgets_baseline_" + slug
	baselinePath := writeTempPackage(t, buildPackage(t, baselineName, compileFixtureVariant(t, "baseline_"+slug), nil))

	w, _ := newWorker(t, env, nil)

	baselineStart := time.Now()
	if _, err := w.run(t.Context(), Args{PackagePath: baselinePath}); err != nil {
		t.Fatalf("baseline run() error: %v", err)
	}
	baseline := time.Since(baselineStart)

	nameA := "widgets_overlap_a_" + slug
	nameB := "widgets_overlap_b_" + slug
	pathA := writeTempPackage(t, buildPackage(t, nameA, compileFixtureVariant(t, "a_"+slug), nil))
	pathB := writeTempPackage(t, buildPackage(t, nameB, compileFixtureVariant(t, "b_"+slug), nil))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	concurrentStart := time.Now()
	wg.Go(func() {
		_, errs[0] = w.run(t.Context(), Args{PackagePath: pathA})
	})
	wg.Go(func() {
		_, errs[1] = w.run(t.Context(), Args{PackagePath: pathB})
	})
	wg.Wait()
	concurrent := time.Since(concurrentStart)

	for i, err := range errs {
		if err != nil {
			t.Fatalf("run() [%d] error: %v", i, err)
		}
	}

	// A 1.7x baseline threshold distinguishes overlapping installs from roughly 2x
	// serialized execution while allowing scheduler noise.
	if threshold := baseline + (baseline * 7 / 10); concurrent > threshold {
		t.Errorf("two concurrent installs of different modules took %s (single-install baseline %s) — want well under 2x baseline (threshold %s); looks serialized", concurrent, baseline, threshold)
	}
}

// Concurrent installs of one name must leave one live module and close the losing pool.
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
	wg.Go(func() {
		_, results[0] = w.run(t.Context(), Args{PackagePath: path1})
	})
	wg.Go(func() {
		_, results[1] = w.run(t.Context(), Args{PackagePath: path2})
	})
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

// Validate subscriptions before irreversible tenant DDL. Check module_schema_versions by
// tenant/module because concurrent suites can create the same table through unrelated
// modules.
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
	_, err := w.run(t.Context(), Args{PackagePath: path})
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

// A rejected install may share the live module's deterministic package path, so cleanup
// must preserve that backing file.
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

	_, err := w.run(t.Context(), Args{PackagePath: path})
	if err == nil || !strings.Contains(err.Error(), "already loaded") {
		t.Fatalf("run() error = %v, want an \"already loaded\" rejection", err)
	}

	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("expected the package file at %q to still exist after an \"already loaded\" rejection, stat error = %v", path, statErr)
	}
}

// A published module remains committed even if rebuilding permissions fails; cleanup must
// not close its live pool.
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
	committed, err := w.publish(t.Context(), m)

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

func TestWorker_Publish_AppendsLoadOrderAfterExistingModules(t *testing.T) {
	env := newTestEnv(t)
	w, reg := newWorker(t, env, map[string]*module.LoadedModule{
		"base": {Status: module.StatusReady, LoadOrder: 0, Manifest: manifest.Manifest{Name: "base", Version: "1.0.0"}},
	})

	m := &module.LoadedModule{Status: module.StatusReady, Manifest: manifest.Manifest{Name: "installed_later", Version: "1.0.0"}}
	if _, err := w.publish(t.Context(), m); err != nil {
		t.Fatalf("publish() error: %v", err)
	}

	got := reg.Snapshot().Modules()["installed_later"].LoadOrder
	if got != 1 {
		t.Errorf("LoadOrder = %d, want 1 (after the one preloaded module)", got)
	}
}

// Reserve the name directly to exercise losing-install cleanup without a scheduling race.
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

	_, err = w.run(t.Context(), Args{PackagePath: path})
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

func TestWorker_Run_BroadcastsModuleInstalledToSucceededTenant(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)

	hub := ws.NewHub()
	conn := connectTenantConn(t, hub, tt.ID)

	wasmBytes := compileFixture(t)
	name := "widgets_" + slug
	pkg := buildPackage(t, name, wasmBytes, nil)
	path := writeTempPackage(t, pkg)

	w, _ := newWorker(t, env, nil)
	w.Hub = hub

	// Compilation and schema sync can exceed a short deadline under the race detector;
	// only the WebSocket read needs a deadline.
	if _, err := w.run(t.Context(), Args{PackagePath: path}); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	readCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var env2 map[string]any
	if err := wsjson.Read(readCtx, conn, &env2); err != nil {
		t.Fatalf("read broadcast envelope: %v", err)
	}
	if env2["channel"] != ws.TenantChannel(tt.ID) {
		t.Errorf("channel = %v, want %q", env2["channel"], ws.TenantChannel(tt.ID))
	}
	if env2["type"] != "module.installed" {
		t.Errorf("type = %v, want %q", env2["type"], "module.installed")
	}
	payload, _ := env2["payload"].(map[string]any)
	if payload["module"] != name {
		t.Errorf("payload[module] = %v, want %q", payload["module"], name)
	}
}

func TestWorker_Run_NoBroadcastToUnrelatedTenantChannel(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)

	hub := ws.NewHub()
	conn := connectTenantConn(t, hub, "some-other-tenant-id")

	wasmBytes := compileFixture(t)
	name := "widgets_" + slug
	pkg := buildPackage(t, name, wasmBytes, nil)
	path := writeTempPackage(t, pkg)

	w, _ := newWorker(t, env, nil)
	w.Hub = hub

	// Unbounded, matching every other run() call in this file — compile+sync
	// can legitimately take longer than any fixed bound under -race.
	if _, err := w.run(t.Context(), Args{PackagePath: path}); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	// This connection is subscribed to a different tenant's channel — it
	// must never see this install's broadcast. Broadcast to other tenants
	// (including any left active in the shared dev database by other
	// tests) may still be racing in the background, so read with a short
	// timeout rather than asserting on connection close.
	readCtx, readCancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer readCancel()
	var env2 map[string]any
	if err := wsjson.Read(readCtx, conn, &env2); err == nil {
		t.Errorf("unexpected message on unrelated tenant channel: %+v", env2)
	}
}

func TestValidateNewModuleSubscriptions_RequiresAnEmittedVersion(t *testing.T) {
	emitter := &module.LoadedModule{
		Status:   module.StatusReady,
		Manifest: manifest.Manifest{Name: "sales", Emits: []manifest.EventDeclaration{{Name: "sales.order.confirmed", Version: 2}}},
	}
	existing := map[string]*module.LoadedModule{"sales": emitter}

	newSubscriber := func(version int, soft ...string) *module.LoadedModule {
		return &module.LoadedModule{Manifest: manifest.Manifest{
			Name:          "inventory",
			SoftDependsOn: soft,
			Subscribes:    []manifest.EventSubscription{{Name: "sales.order.confirmed", Version: version}},
		}}
	}

	if err := validateNewModuleSubscriptions(newSubscriber(2), existing); err != nil {
		t.Errorf("subscription to emitted v2: %v", err)
	}
	if err := validateNewModuleSubscriptions(newSubscriber(1), existing); err == nil || !strings.Contains(err.Error(), "version 1, which no loaded module emits") {
		t.Errorf("subscription to unemitted v1: err = %v, want an unemitted-version error", err)
	}
	if err := validateNewModuleSubscriptions(newSubscriber(1, "sales"), existing); err != nil {
		t.Errorf("unemitted version of a soft dependency's event: %v", err)
	}
}
