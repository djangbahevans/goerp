package jobdispatch

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
	"github.com/vmihailenco/msgpack/v5"
)

type jobIdentityFixture struct {
	admin  *sql.DB
	worker *Worker
	sync   *SyncDispatcher
	roles  *role.Store
	tenant *tenant.Tenant

	schema    string
	userID    string
	contactID string
	managerID string
}

func newJobIdentityFixture(t *testing.T) *jobIdentityFixture {
	t.Helper()

	ctx := t.Context()
	cleanupCtx := context.WithoutCancel(ctx)

	admin, tenants := newTestTenantStore(t)
	tn := newFixtureTenant(t, admin, tenants)
	schema := tenantschema.Name(tn.Slug)
	if err := tenantschema.Create(ctx, admin, tn.Slug); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := tenantschema.Drop(cleanupCtx, admin, tn.Slug); err != nil {
			t.Errorf("drop tenant schema: %v", err)
		}
	})

	roles := role.NewStore(admin)
	if err := roles.Bootstrap(ctx, tn.Slug); err != nil {
		t.Fatal(err)
	}

	userID, contactID := uuid.New().String(), uuid.New().String()
	if err := roles.AddMember(ctx, tn.Slug, userID); err != nil {
		t.Fatal(err)
	}

	if err := roles.SetMemberContact(ctx, tn.Slug, userID, contactID); err != nil {
		t.Fatal(err)
	}

	var managerID string
	if err := admin.QueryRowContext(ctx, "INSERT INTO "+schema+".roles (name) VALUES ('sales_manager') RETURNING id").Scan(&managerID); err != nil {
		t.Fatal(err)
	}

	if err := roles.AssignRole(ctx, tn.Slug, userID, managerID, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.ExecContext(ctx, `CREATE TABLE `+schema+`.widget (
		id UUID PRIMARY KEY DEFAULT uuidv7(),
		name TEXT NOT NULL,
		owner_contact_id UUID,
		seen INTEGER NOT NULL DEFAULT 0,
		tenant_id UUID NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		created_by UUID,
		etag TEXT NOT NULL DEFAULT ''
	);
	ALTER TABLE `+schema+`.widget ENABLE ROW LEVEL SECURITY;
	ALTER TABLE `+schema+`.widget FORCE ROW LEVEL SECURITY;

	CREATE TABLE `+schema+`.delivery_audit (
		user_id TEXT,
		contact_id TEXT,
		roles TEXT,
		sql_names TEXT,
		orm_names TEXT,
		envelope_user_id TEXT
	);
	GRANT SELECT, UPDATE ON `+schema+`.widget TO `+schema+`;
	GRANT INSERT ON `+schema+`.delivery_audit TO `+schema); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.ExecContext(ctx, "INSERT INTO "+schema+".widget (tenant_id, name, owner_contact_id) VALUES ($1, 'own', $2), ($1, 'other', $3)", tn.ID, contactID, uuid.New().String()); err != nil {
		t.Fatal(err)
	}

	// A private login keeps River grants local to this fixture; the shared dev
	// engine_user can lack grants on River tables created by other test clients.
	login := "jobidentity" + uuid.New().String()[:8]
	if _, err := admin.ExecContext(ctx, `
		CREATE ROLE `+login+` LOGIN PASSWORD 'dev' NOSUPERUSER NOBYPASSRLS;
		GRANT engine_user TO `+login+`;
		GRANT SELECT, INSERT, UPDATE ON system.river_job TO `+login+`;
		GRANT USAGE ON SEQUENCE system.river_job_id_seq TO `+login); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if _, err := admin.ExecContext(cleanupCtx, "DROP OWNED BY "+login+"; DROP ROLE "+login); err != nil {
			t.Errorf("drop fixture login: %v", err)
		}
	})

	primary, err := db.New("postgres://" + login + ":dev@localhost:6432/goerp")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = primary.Close() })

	rt, err := wasm.New(&config.Config{
		CompilationCache:            filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes:           64 << 20,
		Environment:                 string(config.Production),
		DBMaxConcurrentTransactions: 1,
	}, primary, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })

	compiled, err := rt.CompileModule(ctx, compileFixture(t, "identityfixture"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })

	pool := rt.NewPool("identityfixture", compiled, wasm.PoolConfig{
		MaxSize:       1,
		BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(cleanupCtx, 5*time.Second) })

	reg := &registry.ModuleRegistry{}
	md := model.Define("widget", model.Table("widget")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("owner_contact_id", model.UUID()).
		Field("seen", model.Integer())

	if _, err := reg.Update(map[string]*module.LoadedModule{
		"identityfixture": {
			Manifest: manifest.Manifest{
				Name: "identityfixture",
				Type: "domain",
				JobTypes: []manifest.JobType{
					{Name: "identity_read"},
					{Name: "identity_enqueue"},
				},
				Provides: map[string]bool{"payment_provider": true},
			},
			Pool:         pool,
			Capabilities: abi.CapDBRead | abi.CapDBWrite | abi.CapJobsEnqueue,
			Status:       module.StatusReady,
			ModelDecls:   []model.ModelDeclaration{*md},
		},
	}); err != nil {
		t.Fatal(err)
	}

	cleanupRiverJobsForTenant(t, admin, tn.ID)

	return &jobIdentityFixture{
		admin:     admin,
		roles:     roles,
		tenant:    tn,
		schema:    schema,
		userID:    userID,
		contactID: contactID,
		managerID: managerID,
		worker: &Worker{
			ModuleRegistry: reg,
			Runtime:        rt,
			TenantStore:    tenants,
			Roles:          roles,
		},
		sync: &SyncDispatcher{
			ModuleRegistry: reg,
			Runtime:        rt,
			Roles:          roles,
		},
	}
}

func (f *jobIdentityFixture) setPolicy(t *testing.T, condition string) {
	t.Helper()

	expr, err := domain.Parse(condition)
	if err != nil {
		t.Fatal(err)
	}

	policy, err := domain.CompileToRLS(expr)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.admin.ExecContext(t.Context(), "DROP POLICY IF EXISTS job_user ON "+f.schema+".widget; CREATE POLICY job_user ON "+f.schema+".widget FOR ALL USING ("+policy+")"); err != nil {
		t.Fatal(err)
	}
}

func (f *jobIdentityFixture) args(userID string) jobqueue.WASMJobArgs {
	return jobqueue.WASMJobArgs{
		ModuleName: "identityfixture",
		JobType:    "identity_read",
		TenantID:   f.tenant.ID,
		UserID:     userID,
	}
}

func (f *jobIdentityFixture) read(t *testing.T, path, userID, contactID, roles, names string) {
	t.Helper()

	if _, err := f.admin.ExecContext(t.Context(), "TRUNCATE "+f.schema+".delivery_audit; UPDATE "+f.schema+".widget SET seen = 0"); err != nil {
		t.Fatal(err)
	}

	args := f.args(userID)
	if path == "sync_provider" {
		status, _, err := f.sync.DispatchJobSync(t.Context(), wasm.SyncJobRequest{
			ModuleName: args.ModuleName,
			JobType:    args.JobType,
			TenantID:   args.TenantID,
			TenantSlug: f.tenant.Slug,
			UserID:     userID,
		})
		if err != nil || status != 0 {
			t.Fatalf("sync dispatch = %d, %v", status, err)
		}
	} else {
		if path == "async_provider" {
			args.ProviderCategory = "payment_provider"
		}

		if err := runWork(t, f.worker, args); err != nil {
			t.Fatal(err)
		}
	}

	var gotUser, gotContact, gotRoles, sqlNames, ormNames, envelopeUser string
	if err := f.admin.QueryRowContext(t.Context(), "SELECT user_id, contact_id, roles, sql_names, orm_names, envelope_user_id FROM "+f.schema+".delivery_audit").Scan(&gotUser, &gotContact, &gotRoles, &sqlNames, &ormNames, &envelopeUser); err != nil {
		t.Fatal(err)
	}

	if gotUser != userID || envelopeUser != userID || gotContact != contactID || gotRoles != roles || sqlNames != names || ormNames != names {
		t.Fatalf("identity/rows = %q %q %q %q %q %q, want %q %q %q %q %q %q", gotUser, envelopeUser, gotContact, gotRoles, sqlNames, ormNames, userID, userID, contactID, roles, names, names)
	}

	var updated string
	if err := f.admin.QueryRowContext(t.Context(), "SELECT COALESCE(string_agg(name, ',' ORDER BY name), '') FROM "+f.schema+".widget WHERE seen = 1").Scan(&updated); err != nil {
		t.Fatal(err)
	}

	if updated != names {
		t.Fatalf("updated %q, want %q", updated, names)
	}
}

func TestJobHandlers_LiveIdentityAndAnonymousRowSecurity(t *testing.T) {
	for _, path := range []string{"queued", "async_provider", "sync_provider"} {
		t.Run(path, func(t *testing.T) {
			f := newJobIdentityFixture(t)

			f.setPolicy(t, "user_has_role('sales_manager')")
			f.read(t, path, f.userID, f.contactID, "sales_manager", "other,own")

			if err := f.roles.RevokeRole(t.Context(), f.tenant.Slug, f.userID, f.managerID); err != nil {
				t.Fatal(err)
			}

			f.read(t, path, f.userID, f.contactID, "", "")

			f.setPolicy(t, "record.owner_contact_id = current_user.contact_id")
			f.read(t, path, f.userID, f.contactID, "", "own")

			if err := f.roles.SetMemberContact(t.Context(), f.tenant.Slug, f.userID, ""); err != nil {
				t.Fatal(err)
			}

			f.read(t, path, f.userID, "", "", "")
			f.read(t, path, "", "", "", "")
		})
	}
}

func TestJobHandlers_ChainedJobsKeepInitiatingUser(t *testing.T) {
	for _, transactional := range []bool{false, true} {
		t.Run(map[bool]string{false: "enqueue", true: "enqueue_tx"}[transactional], func(t *testing.T) {
			f := newJobIdentityFixture(t)
			f.setPolicy(t, "user_has_role('sales_manager')")

			payload, err := msgpack.Marshal(map[string]bool{"transactional": transactional})
			if err != nil {
				t.Fatal(err)
			}

			args := f.args(f.userID)
			args.JobType, args.Payload = "identity_enqueue", payload

			if err := runWork(t, f.worker, args); err != nil {
				t.Fatal(err)
			}

			child := loadWASMJobArgs(t, f.admin, "identityfixture", "identity_read", f.tenant.ID)
			if child.UserID != f.userID {
				t.Fatalf("child user = %q, want %q", child.UserID, f.userID)
			}

			if err := f.roles.RevokeRole(t.Context(), f.tenant.Slug, f.userID, f.managerID); err != nil {
				t.Fatal(err)
			}

			if err := runWork(t, f.worker, child); err != nil {
				t.Fatal(err)
			}

			var names string
			if err := f.admin.QueryRowContext(t.Context(), "SELECT sql_names FROM "+f.schema+".delivery_audit").Scan(&names); err != nil {
				t.Fatal(err)
			}

			if names != "" {
				t.Fatalf("queued child retained revoked access: %q", names)
			}
		})
	}
}

func TestJobHandlers_InactiveMemberCancelledBeforeInvocation(t *testing.T) {
	f := newJobIdentityFixture(t)

	for _, status := range []string{"suspended", "removed"} {
		if status == "removed" {
			if _, err := f.admin.ExecContext(t.Context(), "DELETE FROM "+f.schema+".tenant_members WHERE user_id = $1", f.userID); err != nil {
				t.Fatal(err)
			}
		} else if _, err := f.admin.ExecContext(t.Context(), "UPDATE "+f.schema+".tenant_members SET status = $1 WHERE user_id = $2", status, f.userID); err != nil {
			t.Fatal(err)
		}

		err := runWork(t, f.worker, f.args(f.userID))
		if _, ok := errors.AsType[*river.JobCancelError](err); !ok || !errors.Is(err, role.ErrNotMember) {
			t.Fatalf("error = %v, want membership cancellation", err)
		}
	}

	var count int
	if err := f.admin.QueryRowContext(t.Context(), "SELECT count(*) FROM "+f.schema+".delivery_audit").Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Fatal("handler ran for inactive member")
	}
}

func TestJobHandlers_ScheduledAnonymousJobStaysUnprivileged(t *testing.T) {
	f := newJobIdentityFixture(t)
	f.setPolicy(t, "user_has_role('sales_manager')")

	args := f.args("")
	args.JobType = "identity_enqueue"
	if err := runWork(t, f.worker, args); err != nil {
		t.Fatal(err)
	}

	child := loadWASMJobArgs(t, f.admin, "identityfixture", "identity_read", f.tenant.ID)
	if child.UserID != "" {
		t.Fatalf("scheduled job acquired user %q", child.UserID)
	}

	var scheduledAt time.Time
	if err := f.admin.QueryRowContext(t.Context(), `SELECT scheduled_at FROM system.river_job WHERE args->>'tenant_id' = $1 AND args->>'job_type' = 'identity_read'`, f.tenant.ID).Scan(&scheduledAt); err != nil {
		t.Fatal(err)
	}

	if !scheduledAt.After(time.Now()) {
		t.Fatalf("job was not scheduled in the future: %s", scheduledAt)
	}

	if err := runWork(t, f.worker, child); err != nil {
		t.Fatal(err)
	}

	var userID, names string
	if err := f.admin.QueryRowContext(t.Context(), "SELECT user_id, sql_names FROM "+f.schema+".delivery_audit").Scan(&userID, &names); err != nil {
		t.Fatal(err)
	}

	if userID != "" || names != "" {
		t.Fatalf("anonymous job identity/rows = %q/%q", userID, names)
	}
}

func TestJobHandlers_IdentityLookupFailureIsRetryable(t *testing.T) {
	w, tenantID := newTestWorker(t, buildHandleJobConstStatusModule(0))
	result, err := runRiverWork(t, w, jobqueue.WASMJobArgs{
		ModuleName: testModuleName,
		JobType:    testJobType,
		TenantID:   tenantID,
		UserID:     uuid.New().String(),
	})
	if err == nil {
		t.Fatal("job ran without an identity resolver")
	}

	if _, cancelled := errors.AsType[*river.JobCancelError](err); cancelled {
		t.Fatalf("identity lookup failure cancelled job: %v", err)
	}

	if result.Job.State != "retryable" && result.Job.State != "available" {
		t.Fatalf("job state = %s, want retryable", result.Job.State)
	}
}
