package tenantsync

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// widgetModuleWithSKU/widgetModuleWithoutSKU are the same module before
// and after dropping its "sku" column — DropColumn is always blocked
// (classify.go), the scenario goerp#292's accept flow exists for.
func widgetModuleWithSKU(t *testing.T) *module.LoadedModule {
	t.Helper()
	return loadedModule(t, "sales",
		*model.Define("sales.widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()).
			Field("sku", model.Char(40).Required()),
	)
}

func widgetModuleWithoutSKU(t *testing.T, version string) *module.LoadedModule {
	t.Helper()
	mod := loadedModule(t, "sales",
		*model.Define("sales.widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	)
	mod.Manifest.Version = version
	return mod
}

func newTestRegistry(t *testing.T, mod *module.LoadedModule) *registry.ModuleRegistry {
	t.Helper()
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{mod.Manifest.Name: mod}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	return reg
}

// newTestJobClient builds a real River client against the same Postgres
// instance testEnv uses — needed only by tests that call Admin.Accept/
// StartSync, since those enqueue a real job row. Never started: these
// tests verify the enqueue and the resync logic (SyncOneAccepted)
// directly, not the real queue actually picking the job up.
func newTestJobClient(t *testing.T) *river.Client[pgx.Tx] {
	t.Helper()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, localPostgresDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := jobqueue.Migrate(ctx, pool); err != nil {
		t.Fatalf("jobqueue.Migrate: %v", err)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &SyncWorker{})
	river.AddWorker(workers, &AcceptResyncWorker{})

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{jobqueue.QueueAdmin: {MaxWorkers: 1}},
		Workers: workers,
	})
	if err != nil {
		t.Fatalf("river.NewClient: %v", err)
	}
	return client
}

func TestAdmin_DiffReportsBlockedColumnDropWithHash(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)

	v1 := widgetModuleWithSKU(t)
	if err := SyncOne(context.Background(), env.pool, env.diffEngine, tt, v1); err != nil {
		t.Fatalf("initial SyncOne() error: %v", err)
	}

	v2 := widgetModuleWithoutSKU(t, "1.1.0")
	reg := newTestRegistry(t, v2)
	admin := NewAdmin(env.tenantStore, reg, env.pool, env.diffEngine, nil, jobqueue.QueueAdmin)

	version, safe, deferred, blocked, err := admin.Diff(context.Background(), slug, "sales", false)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if version != "1.1.0" {
		t.Errorf("version = %q, want %q", version, "1.1.0")
	}
	_ = safe
	_ = deferred
	if len(blocked) != 1 {
		t.Fatalf("blocked = %+v, want exactly one drop_column change", blocked)
	}
	if blocked[0].Kind != "drop_column" {
		t.Errorf("blocked[0].Kind = %q, want %q", blocked[0].Kind, "drop_column")
	}
	if blocked[0].Hash == "" {
		t.Error("blocked[0].Hash is empty, want a non-empty stable identifier")
	}
	if blocked[0].Detail != "" {
		t.Errorf("non-verbose blocked[0].Detail = %q, want empty", blocked[0].Detail)
	}

	_, _, _, verboseBlocked, err := admin.Diff(context.Background(), slug, "sales", true)
	if err != nil {
		t.Fatalf("Diff(verbose) error: %v", err)
	}
	if len(verboseBlocked) != 1 || verboseBlocked[0].Detail == "" {
		t.Errorf("verbose blocked = %+v, want exactly one entry with a non-empty Detail", verboseBlocked)
	}
}

func TestAdmin_AcceptRecordsAcceptanceAndUnblocksResync(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)

	v1 := widgetModuleWithSKU(t)
	if err := SyncOne(context.Background(), env.pool, env.diffEngine, tt, v1); err != nil {
		t.Fatalf("initial SyncOne() error: %v", err)
	}

	v2 := widgetModuleWithoutSKU(t, "1.1.0")
	reg := newTestRegistry(t, v2)
	jobClient := newTestJobClient(t)
	admin := NewAdmin(env.tenantStore, reg, env.pool, env.diffEngine, jobClient, jobqueue.QueueAdmin)

	acceptanceIDs, jobID, err := admin.Accept(context.Background(), slug, "sales", "verified manually", "test-operator")
	if err != nil {
		t.Fatalf("Accept() error: %v", err)
	}
	if len(acceptanceIDs) != 1 {
		t.Fatalf("acceptanceIDs = %v, want exactly one", acceptanceIDs)
	}
	if jobID == "" {
		t.Error("jobID is empty")
	}

	accepted, err := env.pool.AcceptedHashes(context.Background(), tt.ID, "sales", "1.1.0")
	if err != nil {
		t.Fatalf("AcceptedHashes() error: %v", err)
	}
	if len(accepted) != 1 {
		t.Fatalf("AcceptedHashes() = %v, want exactly one entry", accepted)
	}

	// SyncOneAccepted is what AcceptResyncWorker.Work calls — verify it
	// actually drops the column now that its hash is accepted.
	if err := SyncOneAccepted(context.Background(), env.pool, env.diffEngine, tt, v2, accepted); err != nil {
		t.Fatalf("SyncOneAccepted() error: %v", err)
	}

	var exists bool
	err = env.conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'widgets' AND column_name = 'sku')",
		"tenant_"+slug,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check column existence: %v", err)
	}
	if exists {
		t.Error("sku column still exists after SyncOneAccepted — accepted blocked change was not applied")
	}
}

func TestAdmin_AcceptWithNothingBlockedErrors(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)

	v1 := widgetModuleWithSKU(t)
	if err := SyncOne(context.Background(), env.pool, env.diffEngine, tt, v1); err != nil {
		t.Fatalf("initial SyncOne() error: %v", err)
	}

	// Same version, no changes at all this time — nothing blocked.
	reg := newTestRegistry(t, v1)
	jobClient := newTestJobClient(t)
	admin := NewAdmin(env.tenantStore, reg, env.pool, env.diffEngine, jobClient, jobqueue.QueueAdmin)

	_, _, err := admin.Accept(context.Background(), slug, "sales", "no-op", "test-operator")
	if !errors.Is(err, ErrNothingBlocked) {
		t.Fatalf("Accept() error = %v, want ErrNothingBlocked", err)
	}
}

// newTestJobClientWithoutAcceptWorker mirrors newTestJobClient but never
// registers AcceptResyncWorker — river.Client.Insert rejects an
// unregistered kind, the deterministic way to reproduce Accept's own
// "acceptance rows written, then job enqueue fails" path.
func newTestJobClientWithoutAcceptWorker(t *testing.T) *river.Client[pgx.Tx] {
	t.Helper()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, localPostgresDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := jobqueue.Migrate(ctx, pool); err != nil {
		t.Fatalf("jobqueue.Migrate: %v", err)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &SyncWorker{})

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{jobqueue.QueueAdmin: {MaxWorkers: 1}},
		Workers: workers,
	})
	if err != nil {
		t.Fatalf("river.NewClient: %v", err)
	}
	return client
}

func TestAdmin_AcceptJobEnqueueFailureReportsAcceptanceIDsAndDoesNotDuplicate(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)

	v1 := widgetModuleWithSKU(t)
	if err := SyncOne(context.Background(), env.pool, env.diffEngine, tt, v1); err != nil {
		t.Fatalf("initial SyncOne() error: %v", err)
	}

	v2 := widgetModuleWithoutSKU(t, "1.1.0")
	reg := newTestRegistry(t, v2)
	brokenJobClient := newTestJobClientWithoutAcceptWorker(t)
	admin := NewAdmin(env.tenantStore, reg, env.pool, env.diffEngine, brokenJobClient, jobqueue.QueueAdmin)

	acceptanceIDs, jobID, err := admin.Accept(context.Background(), slug, "sales", "verified manually", "test-operator")
	if err == nil {
		t.Fatal("Accept() error = nil, want the job-enqueue failure to surface")
	}
	if jobID != "" {
		t.Errorf("jobID = %q, want empty on enqueue failure", jobID)
	}
	if len(acceptanceIDs) != 1 {
		t.Fatalf("acceptanceIDs = %v, want exactly one (the row was written before enqueue failed)", acceptanceIDs)
	}
	if !strings.Contains(err.Error(), acceptanceIDs[0]) {
		t.Errorf("error %q does not mention acceptance id %q — an operator retrying has no way to know it already exists", err.Error(), acceptanceIDs[0])
	}

	// A retry (even with a working job client this time) must not create
	// a second acceptance row for the same still-blocked hash.
	workingJobClient := newTestJobClient(t)
	admin2 := NewAdmin(env.tenantStore, reg, env.pool, env.diffEngine, workingJobClient, jobqueue.QueueAdmin)
	acceptanceIDs2, jobID2, err := admin2.Accept(context.Background(), slug, "sales", "verified manually", "test-operator")
	if err != nil {
		t.Fatalf("retry Accept() error: %v", err)
	}
	if jobID2 == "" {
		t.Error("retry jobID is empty")
	}
	if len(acceptanceIDs2) != 0 {
		t.Errorf("retry acceptanceIDs = %v, want none new (the hash was already accepted by the first call)", acceptanceIDs2)
	}

	var count int
	if err := env.conn.QueryRow(
		"SELECT COUNT(*) FROM system.schema_sync_acceptances WHERE tenant_id = $1 AND module_name = 'sales'",
		tt.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count acceptance rows: %v", err)
	}
	if count != 1 {
		t.Errorf("acceptance row count = %d, want exactly 1 (no duplicate from the retry)", count)
	}
}

func TestAdmin_StatusPendingFilterFindsBlockedModule(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)

	v1 := widgetModuleWithSKU(t)
	if err := SyncOne(context.Background(), env.pool, env.diffEngine, tt, v1); err != nil {
		t.Fatalf("initial SyncOne() error: %v", err)
	}

	v2 := widgetModuleWithoutSKU(t, "1.1.0")
	reg := newTestRegistry(t, v2)
	admin := NewAdmin(env.tenantStore, reg, env.pool, env.diffEngine, nil, jobqueue.QueueAdmin)

	pending, err := admin.Status(context.Background(), slug, "", "pending")
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if len(pending) != 1 || pending[0].TenantSlug != slug || pending[0].ModuleName != "sales" {
		t.Fatalf("pending = %+v, want exactly one row for tenant %q module sales", pending, slug)
	}

	// A module with nothing blocked (same version, no changes) must not
	// show up under "pending".
	okOnly, err := admin.Status(context.Background(), slug, "", "ok")
	if err != nil {
		t.Fatalf("Status(ok) error: %v", err)
	}
	if len(okOnly) != 1 {
		t.Errorf("Status(ok) = %+v, want the one already-synced row", okOnly)
	}
}

func TestAdmin_DiffUnknownTenantReturnsWrappedErrTenantNotFound(t *testing.T) {
	env := newTestEnv(t)
	reg := newTestRegistry(t, widgetModuleWithSKU(t))
	admin := NewAdmin(env.tenantStore, reg, env.pool, env.diffEngine, nil, jobqueue.QueueAdmin)

	_, _, _, _, err := admin.Diff(context.Background(), "does-not-exist", "sales", false)
	if !errors.Is(err, tenant.ErrTenantNotFound) {
		t.Fatalf("Diff() error = %v, want it to wrap tenant.ErrTenantNotFound", err)
	}
}

func TestAdmin_DiffUnloadedModuleReturnsErrModuleNotLoaded(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	env.activeTenant(t, slug)
	reg := newTestRegistry(t, widgetModuleWithSKU(t)) // registered as "sales"
	admin := NewAdmin(env.tenantStore, reg, env.pool, env.diffEngine, nil, jobqueue.QueueAdmin)

	_, _, _, _, err := admin.Diff(context.Background(), slug, "does-not-exist", false)
	if !errors.Is(err, ErrModuleNotLoaded) {
		t.Fatalf("Diff() error = %v, want it to wrap ErrModuleNotLoaded", err)
	}
}
