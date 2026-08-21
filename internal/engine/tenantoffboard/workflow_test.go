package tenantoffboard

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/search"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
const localRedisAddr = "localhost:6379"
const localMeilisearchURL = "http://localhost:7700"
const localMeilisearchKey = "2f14b775804ecaf5dc4084d32aa034a7"

// slugCounter guarantees uniqueSlug never repeats within a test run —
// same reasoning as tenantprovision's own slugCounter (a bare time.Now()
// collided in practice between rapid successive calls).
var slugCounter atomic.Uint64

func uniqueSlug(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("offb%d%d", time.Now().UnixNano(), slugCounter.Add(1))
}

func widgetsSearchModule() *module.LoadedModule {
	return &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: manifest.Manifest{
			Name:          "widgets",
			Version:       "1.0.0",
			SearchIndexes: []manifest.SearchIndex{{Name: "widgets", Resource: "sales.widget"}},
		},
	}
}

// testEnv wires every real dependency OffboardTenantWorkflow's activities
// need — the same components engine.New() constructs — against the real
// compose.dev.yml Postgres, Redis, Meilisearch, and Temporal, no fakes.
// Meilisearch and local storage aren't strictly required for every test
// (both are warn-only dependencies in a real engine), so a caller that
// doesn't need them can pass mods=nil and ignore env.searchClient/
// env.storageBackend being present anyway — the fixtures are always live
// since search/storage are always reachable in this dev sandbox.
type testEnv struct {
	conn           *sql.DB
	tenantStore    *tenant.Store
	filesStore     *files.Store
	cacheClient    *cache.Client
	searchClient   *search.Client
	storageBackend storage.Backend
	activities     *Activities
	temporalClient *temporal.Client
	taskQueue      string
}

func newTestEnv(t *testing.T, mods map[string]*module.LoadedModule) *testEnv {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	filesStore := files.NewStore(conn)

	cacheCtx, cacheCancel := context.WithTimeout(ctx, 2*time.Second)
	defer cacheCancel()
	cacheClient, err := cache.New(cacheCtx, cache.Config{Addr: localRedisAddr, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at %s (start compose.dev.yml): %v", localRedisAddr, err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	searchClient, err := search.New(localMeilisearchURL, localMeilisearchKey)
	if err != nil {
		t.Skipf("meilisearch not reachable at %s (start compose.dev.yml): %v", localMeilisearchURL, err)
	}

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	storageBackend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New() error: %v", err)
	}

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(mods); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}

	activities := NewActivities(tenantStore, filesStore, cacheClient, searchClient, storageBackend, conn, reg)

	t.Setenv("GOERP_TEMPORAL_HOST_PORT", "127.0.0.1:7233")
	t.Setenv("GOERP_TEMPORAL_NAMESPACE", "default")
	temporalCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	temporalClient, err := temporal.New(temporalCtx)
	if err != nil {
		t.Skipf("temporal not reachable at 127.0.0.1:7233 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { temporalClient.Close() })

	taskQueue := "test-tenantoffboard-" + uniqueSlug(t)
	w := temporalClient.NewWorker(taskQueue, worker.Options{})
	w.RegisterWorkflow(Workflow)
	w.RegisterActivity(activities)
	if err := w.Start(); err != nil {
		t.Fatalf("worker.Start() error: %v", err)
	}
	t.Cleanup(w.Stop)
	if err := temporalClient.WaitForPollers(ctx, taskQueue); err != nil {
		t.Fatalf("WaitForPollers() error: %v", err)
	}

	return &testEnv{
		conn:           conn,
		tenantStore:    tenantStore,
		filesStore:     filesStore,
		cacheClient:    cacheClient,
		searchClient:   searchClient,
		storageBackend: storageBackend,
		activities:     activities,
		temporalClient: temporalClient,
		taskQueue:      taskQueue,
	}
}

// activeTenant creates a tenant, activates it, and bootstraps its schema
// plus files table — the state a real, already-provisioned tenant is in
// by the time anyone offboards it. Registers cleanup for both the row and
// the schema.
func (e *testEnv) activeTenant(t *testing.T, slug string) *tenant.Tenant {
	t.Helper()
	ctx := context.Background()

	tt, err := e.tenantStore.CreateTenant(ctx, slug, "Offboard Test")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID)
		_, _ = e.conn.Exec("DROP SCHEMA IF EXISTS " + tenantschema.Name(slug) + " CASCADE")
	})

	if _, err := e.tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate tenant: %v", err)
	}

	if _, err := e.conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+tenantschema.Name(slug)); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	if err := e.filesStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("files Bootstrap() error: %v", err)
	}

	return tt
}

func (e *testEnv) runWorkflow(t *testing.T, ctx context.Context, input Input) error {
	t.Helper()
	run, err := e.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: e.taskQueue}, Workflow, input)
	if err != nil {
		t.Fatalf("ExecuteWorkflow() error: %v", err)
	}
	return run.Get(ctx, nil)
}

func schemaExists(t *testing.T, conn *sql.DB, slug string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)",
		"tenant_"+slug,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check schema existence: %v", err)
	}
	return exists
}

func TestOffboardTenantWorkflow_EndToEnd(t *testing.T) {
	env := newTestEnv(t, map[string]*module.LoadedModule{"widgets": widgetsSearchModule()})
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)
	ctx := context.Background()

	cacheKey := tt.ID + ":offboard-test:marker"
	if err := env.cacheClient.SetWithTTL(ctx, cacheKey, "1", time.Minute); err != nil {
		t.Fatalf("seed cache key: %v", err)
	}
	t.Cleanup(func() { _ = env.cacheClient.Delete(context.Background(), cacheKey) })

	if _, err := env.storageBackend.Upload(ctx, "attachments/"+tt.ID+"/2026/01/f1.txt", strings.NewReader("hello"), storage.UploadOptions{}); err != nil {
		t.Fatalf("upload fixture file: %v", err)
	}
	if err := env.filesStore.Insert(ctx, slug, files.InsertRow{
		ID: "01900000-0000-7000-8000-000000000001", TenantID: tt.ID,
		StorageKey:   "attachments/" + tt.ID + "/2026/01/f1.txt",
		OriginalName: "f1.txt", ContentType: "text/plain", SizeBytes: 5,
		ChecksumSHA256: "irrelevant", Purpose: "attachments",
	}); err != nil {
		t.Fatalf("insert files row: %v", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := env.runWorkflow(t, runCtx, Input{TenantID: tt.ID, TenantSlug: slug, GracePeriod: 100 * time.Millisecond}); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	got, err := env.tenantStore.GetBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("GetBySlug() error: %v", err)
	}
	if got.Status != tenant.StatusDeleted {
		t.Errorf("Status = %q, want %q", got.Status, tenant.StatusDeleted)
	}
	if got.DeletedAt == nil {
		t.Error("expected DeletedAt to be set")
	}

	if schemaExists(t, env.conn, slug) {
		t.Error("expected tenant schema to have been dropped")
	}

	exists, err := env.cacheClient.Exists(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Exists() error: %v", err)
	}
	if exists {
		t.Error("expected the tenant's cache key to have been flushed")
	}

	if _, _, err := env.storageBackend.Download(ctx, "attachments/"+tt.ID+"/2026/01/f1.txt"); err == nil {
		t.Error("expected the tenant's storage file to have been deleted")
	}
}

func TestOffboardTenantWorkflow_CancelledDuringGracePeriodDeletesNothing(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)
	ctx := context.Background()

	run, err := env.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: env.taskQueue}, Workflow, Input{
		TenantID: tt.ID, TenantSlug: slug, GracePeriod: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("ExecuteWorkflow() error: %v", err)
	}

	// Give MarkOffboarding time to actually run before cancelling.
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := env.tenantStore.GetBySlug(ctx, slug)
		if err != nil {
			t.Fatalf("GetBySlug() error: %v", err)
		}
		if got.Status == tenant.StatusOffboarding {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tenant never reached StatusOffboarding (last status: %q)", got.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := env.tenantStore.CancelOffboarding(ctx, slug); err != nil {
		t.Fatalf("CancelOffboarding() error: %v", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := run.Get(runCtx, nil); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	got, err := env.tenantStore.GetBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("GetBySlug() error: %v", err)
	}
	if got.Status != tenant.StatusActive {
		t.Errorf("Status = %q, want %q (cancel should have restored it)", got.Status, tenant.StatusActive)
	}
	if !schemaExists(t, env.conn, slug) {
		t.Error("expected tenant schema to survive a cancelled offboard")
	}
}
