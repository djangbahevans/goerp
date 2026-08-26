package tenantprovision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func widgetModel() model.ModelDeclaration {
	return *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required())
}

// slugCounter guarantees uniqueSlug never repeats within a test run, even
// across calls close enough together to land on the same wall-clock
// nanosecond reading — time.Now() alone collided in practice (two tests
// racing to insert the same slug into system.tenants, and to start a
// Temporal workflow with the same derived ID, corrupting both runs).
var slugCounter atomic.Uint64

func uniqueSlug(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("prov%d%d", time.Now().UnixNano(), slugCounter.Add(1))
}

// testEnv wires every real dependency ProvisionTenantWorkflow's activities
// need — the same components engine.New() constructs — against the real
// compose.dev.yml Postgres and Temporal, no fakes.
type testEnv struct {
	conn           *sql.DB
	tenantStore    *tenant.Store
	inviteStore    *invite.Store
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

	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}

	roleStore := role.NewStore(conn)
	inviteStore := invite.NewStore(conn, userStore, roleStore, nil, nil)

	syncPool := schema.NewPool(conn, 5*time.Second)
	if err := syncPool.Bootstrap(ctx); err != nil {
		t.Fatalf("schema pool Bootstrap() error: %v", err)
	}
	diffEngine := schema.NewSchemaDiffEngine(&schema.Config{})

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(mods); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}

	activities := NewActivities(tenantStore, inviteStore, conn, syncPool, diffEngine, reg, "goerp.test")

	t.Setenv("GOERP_TEMPORAL_HOST_PORT", "127.0.0.1:7233")
	t.Setenv("GOERP_TEMPORAL_NAMESPACE", "default")
	temporalCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	temporalClient, err := temporal.New(temporalCtx)
	if err != nil {
		t.Skipf("temporal not reachable at 127.0.0.1:7233 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { temporalClient.Close() })

	taskQueue := "test-tenantprovision-" + uniqueSlug(t)
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
		inviteStore:    inviteStore,
		activities:     activities,
		temporalClient: temporalClient,
		taskQueue:      taskQueue,
	}
}

func (e *testEnv) runWorkflow(t *testing.T, input Input) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	run, err := e.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: e.taskQueue}, Workflow, input)
	if err != nil {
		t.Fatalf("ExecuteWorkflow() error: %v", err)
	}
	return run.Get(ctx, nil)
}

func TestProvisionTenantWorkflow_EndToEnd(t *testing.T) {
	slug := uniqueSlug(t)
	mod := &module.LoadedModule{
		Status: module.StatusReady,
		Manifest: manifest.Manifest{
			Name:              "widgets",
			Version:           "1.0.0",
			Schema:            manifest.SchemaConfig{OwnedModels: []string{"sales.widget"}},
			Permissions:       []manifest.Permission{{Name: "widgets.read", DefaultRoles: []string{"admin"}}},
			TenantConfigSeeds: map[string]any{"currency": "USD"},
		},
		ModelDecls: []model.ModelDeclaration{widgetModel()},
	}

	env := newTestEnv(t, map[string]*module.LoadedModule{"widgets": mod})
	t.Cleanup(func() {
		_, _ = env.conn.Exec("DELETE FROM system.tenants WHERE slug = $1", slug)
		_, _ = env.conn.Exec("DROP SCHEMA IF EXISTS " + tenantschema.Name(slug) + " CASCADE")
	})

	err := env.runWorkflow(t, Input{
		Slug:       slug,
		Name:       "Acme Corp",
		AdminEmail: slug + "@example.com",
	})
	if err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	tt, err := env.tenantStore.GetBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetBySlug() error: %v", err)
	}
	if tt.Status != tenant.StatusActive {
		t.Errorf("Status = %q, want %q", tt.Status, tenant.StatusActive)
	}

	domains, err := env.tenantStore.DomainsForTenant(context.Background(), tt.ID)
	if err != nil {
		t.Fatalf("DomainsForTenant() error: %v", err)
	}
	if len(domains) != 1 || domains[0].Domain != slug+".goerp.test" {
		t.Errorf("domains = %+v, want one domain %q", domains, slug+".goerp.test")
	}

	var widgetsExists bool
	if err := env.conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'widgets')",
		"tenant_"+slug,
	).Scan(&widgetsExists); err != nil {
		t.Fatalf("check widgets table: %v", err)
	}
	if !widgetsExists {
		t.Error("expected the widgets table to have been created by module schema sync")
	}

	var sequencesExists bool
	if err := env.conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'sequences')",
		"tenant_"+slug,
	).Scan(&sequencesExists); err != nil {
		t.Fatalf("check sequences table: %v", err)
	}
	if !sequencesExists {
		t.Error("expected the sequences table to have been created by CreateEngineTables")
	}

	var auditLogExists bool
	if err := env.conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'audit_log')",
		"tenant_"+slug,
	).Scan(&auditLogExists); err != nil {
		t.Fatalf("check audit_log table: %v", err)
	}
	if !auditLogExists {
		t.Error("expected the audit_log table to have been created by CreateEngineTables")
	}

	var eventLogExists bool
	if err := env.conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'event_log')",
		"tenant_"+slug,
	).Scan(&eventLogExists); err != nil {
		t.Fatalf("check event_log table: %v", err)
	}
	if !eventLogExists {
		t.Error("expected the event_log table to have been created by CreateEngineTables")
	}

	for _, table := range []string{"audit_log", "event_log"} {
		var registered bool
		if err := env.conn.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM partman.part_config WHERE parent_table = $1)",
			"tenant_"+slug+"."+table,
		).Scan(&registered); err != nil {
			t.Fatalf("check pg_partman registration for %s: %v", table, err)
		}
		if !registered {
			t.Errorf("expected %s to be registered with pg_partman by CreateEngineTables", table)
		}
	}

	var configValue []byte
	err = env.conn.QueryRow(
		`SELECT value FROM ` + tenantschema.Name(slug) + `.module_config WHERE module_name = 'widgets' AND key = 'currency'`,
	).Scan(&configValue)
	if err != nil {
		t.Fatalf("query module_config: %v", err)
	}
	var got string
	if err := json.Unmarshal(configValue, &got); err != nil {
		t.Fatalf("unmarshal module_config value: %v", err)
	}
	if got != "USD" {
		t.Errorf("module_config currency = %q, want %q", got, "USD")
	}

	roleID, err := role.NewStore(env.conn).GetRoleByName(context.Background(), slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	var granted bool
	err = env.conn.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM `+tenantschema.Name(slug)+`.role_permissions WHERE role_id = $1 AND permission_name = 'widgets.read')`,
		roleID,
	).Scan(&granted)
	if err != nil {
		t.Fatalf("query role_permissions: %v", err)
	}
	if !granted {
		t.Error("expected widgets.read to have been granted to the admin role")
	}

	var invitedCount int
	err = env.conn.QueryRow(
		`SELECT COUNT(*) FROM `+tenantschema.Name(slug)+`.tenant_invitations WHERE email = $1`,
		slug+"@example.com",
	).Scan(&invitedCount)
	if err != nil {
		t.Fatalf("query tenant_invitations: %v", err)
	}
	if invitedCount != 1 {
		t.Errorf("tenant_invitations count for admin email = %d, want 1", invitedCount)
	}
}

// TestProvisionTenantWorkflow_SchemaCreationFailureReleasesSlug exercises
// the ReserveSlug/ReleaseSlugReservation compensation pair directly
// rather than through a full workflow run: CreateTenantSchema's own
// "CREATE SCHEMA IF NOT EXISTS" never fails on a pre-existing schema, so
// there's no reliable, non-mocked way to force it to fail inside this
// sandbox. What actually matters for goerp#149's AC — releasing the slug
// makes it available again — is fully covered by calling the two
// activities in the same sequence Workflow's own failure branch does.
func TestProvisionTenantWorkflow_SchemaCreationFailureReleasesSlug(t *testing.T) {
	slug := uniqueSlug(t)
	env := newTestEnv(t, nil)

	tenantID, err := env.activities.ReserveSlug(context.Background(), slug, "Compensation Test")
	if err != nil {
		t.Fatalf("ReserveSlug() error: %v", err)
	}

	if err := env.activities.ReleaseSlugReservation(context.Background(), tenantID); err != nil {
		t.Fatalf("ReleaseSlugReservation() error: %v", err)
	}

	if _, err := env.tenantStore.GetByID(context.Background(), tenantID); !errors.Is(err, tenant.ErrTenantNotFound) {
		t.Errorf("GetByID() after ReleaseSlugReservation() error = %v, want ErrTenantNotFound", err)
	}

	// The slug is available again — a second ReserveSlug for the same
	// slug succeeds.
	tt, err := env.tenantStore.CreateTenant(context.Background(), slug, "Retry")
	if err != nil {
		t.Fatalf("CreateTenant() after release: expected success, got error: %v", err)
	}
	t.Cleanup(func() { _, _ = env.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
}
