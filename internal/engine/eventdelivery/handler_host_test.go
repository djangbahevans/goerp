package eventdelivery

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/event"
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

type eventHostFixture struct {
	admin, reader                        *sql.DB
	runtime                              *wasm.Runtime
	worker                               *SubscriberDeliveryWorker
	sync                                 *SyncDispatcher
	roles                                *role.Store
	tenant                               *tenant.Tenant
	schema, userID, contactID, managerID string
}

func newEventHostFixture(t *testing.T) *eventHostFixture {
	t.Helper()

	ctx := t.Context()
	cleanupCtx := context.WithoutCancel(ctx)

	admin, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	tenants := tenant.NewStore(admin)
	if err := tenants.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}

	slug := "eventhost" + uuid.New().String()[:8]
	tn := newTestTenant(t, tenants, admin, slug)
	schema := tenantschema.Name(slug)

	roles := role.NewStore(admin)
	if err := roles.Bootstrap(ctx, slug); err != nil {
		t.Fatal(err)
	}

	userID, contactID := uuid.New().String(), uuid.New().String()
	if err := roles.AddMember(ctx, slug, userID); err != nil {
		t.Fatal(err)
	}
	if err := roles.SetMemberContact(ctx, slug, userID, contactID); err != nil {
		t.Fatal(err)
	}

	var managerID string
	if err := admin.QueryRowContext(ctx, "INSERT INTO "+schema+".roles (name) VALUES ('sales_manager') RETURNING id").Scan(&managerID); err != nil {
		t.Fatal(err)
	}
	if err := roles.AssignRole(ctx, slug, userID, managerID, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.ExecContext(ctx, `CREATE TABLE `+schema+`.widget (
		id UUID PRIMARY KEY DEFAULT uuidv7(), name TEXT NOT NULL, owner_contact_id UUID, seen INTEGER NOT NULL DEFAULT 0,
		tenant_id UUID NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ, created_by UUID, etag TEXT NOT NULL DEFAULT ''
	); ALTER TABLE `+schema+`.widget ENABLE ROW LEVEL SECURITY; ALTER TABLE `+schema+`.widget FORCE ROW LEVEL SECURITY;
	CREATE TABLE `+schema+`.delivery_audit (user_id TEXT, contact_id TEXT, roles TEXT, sql_names TEXT, orm_names TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "INSERT INTO "+schema+".widget (tenant_id, name, owner_contact_id) VALUES ($1, 'own', $2), ($1, 'other', $3)", tn.ID, contactID, uuid.New().String()); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.ExecContext(ctx, "CREATE ROLE "+schema+" LOGIN PASSWORD 'dev' NOSUPERUSER NOBYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(cleanupCtx, "DROP OWNED BY "+schema+"; DROP ROLE "+schema); err != nil {
			t.Errorf("drop fixture role: %v", err)
		}
	})
	if _, err := admin.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+schema+"; GRANT SELECT, UPDATE ON "+schema+".widget TO "+schema+"; GRANT INSERT ON "+schema+".delivery_audit TO "+schema); err != nil {
		t.Fatal(err)
	}

	reader, err := db.New("postgres://tenant_" + slug + ":dev@localhost:6432/goerp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	rt, err := wasm.New(&config.Config{CompilationCache: filepath.Join(t.TempDir(), "cache"), PoolMaxMemoryByes: 64 << 20,
		Environment: string(config.Production), DBMaxConcurrentTransactions: 1}, reader, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })

	wasmPath := filepath.Join(t.TempDir(), "eventfixture.wasm")
	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/eventfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile event fixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	compiled, err := rt.CompileModule(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })

	pool := rt.NewPool("eventfixture", compiled, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: time.Second})
	t.Cleanup(func() { pool.DrainAndClose(cleanupCtx, 5*time.Second) })

	md := model.Define("widget", model.Table("widget")).WithStandardFields().Field("name", model.Text().Required()).Field("owner_contact_id", model.UUID()).Field("seen", model.Integer())
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{"eventfixture": {
		Manifest: manifest.Manifest{Name: "eventfixture", Type: "standard", Subscribes: []manifest.EventSubscription{{Name: testEventName, Handler: testHandlerName, Async: true}}},
		Pool:     pool, Capabilities: abi.CapDBRead | abi.CapDBWrite, Status: module.StatusReady, ModelDecls: []model.ModelDeclaration{*md},
	}}); err != nil {
		t.Fatal(err)
	}

	invoker := &HandlerInvoker{Runtime: rt, TenantStore: tenants, Roles: roles}

	return &eventHostFixture{admin: admin, reader: reader, runtime: rt,
		worker: &SubscriberDeliveryWorker{ModuleRegistry: reg, Invoker: invoker}, sync: &SyncDispatcher{ModuleRegistry: reg, Invoker: invoker},
		roles: roles, tenant: tn, schema: schema, userID: userID, contactID: contactID, managerID: managerID}
}

func (f *eventHostFixture) deliver(t *testing.T, async bool, userID, mode string) (int32, error) {
	t.Helper()

	payload, err := msgpack.Marshal(map[string]string{"mode": mode})
	if err != nil {
		t.Fatal(err)
	}

	env := event.Envelope{ID: uuid.New().String(), Name: testEventName, Version: 1, EmitterModule: "emitter",
		TenantID: f.tenant.ID, UserID: userID, TraceID: "event-trace", Payload: payload}

	if !async {
		data, err := env.Marshal()
		if err != nil {
			t.Fatal(err)
		}

		return f.sync.DispatchSync(t.Context(), "eventfixture", testHandlerName, data)
	}

	err = runSubscriberWork(t, f.worker, jobqueue.SubscriberDeliveryArgs{EventID: env.ID, EventName: env.Name, EventVersion: env.Version,
		EmitterModule: env.EmitterModule, TenantID: env.TenantID, UserID: env.UserID, TraceID: env.TraceID, Payload: env.Payload,
		ModuleName: "eventfixture", HandlerName: testHandlerName})

	return 0, err
}

func (f *eventHostFixture) setPolicy(t *testing.T, condition string) {
	t.Helper()

	expr, err := domain.Parse(condition)
	if err != nil {
		t.Fatal(err)
	}

	policy, err := domain.CompileToRLS(expr)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.admin.ExecContext(t.Context(), "DROP POLICY IF EXISTS event_user ON "+f.schema+".widget; CREATE POLICY event_user ON "+f.schema+".widget FOR ALL USING ("+policy+")"); err != nil {
		t.Fatal(err)
	}
}

func (f *eventHostFixture) read(t *testing.T, async bool, userID, contactID, roles, names string) {
	t.Helper()

	if _, err := f.admin.ExecContext(t.Context(), "TRUNCATE "+f.schema+".delivery_audit; UPDATE "+f.schema+".widget SET seen = 0"); err != nil {
		t.Fatal(err)
	}

	status, err := f.deliver(t, async, userID, "read")
	if err != nil || status != 0 {
		t.Fatalf("deliver = %d, %v", status, err)
	}

	var gotUser, gotContact, gotRoles, sqlNames, ormNames string
	if err := f.admin.QueryRowContext(t.Context(), "SELECT user_id, contact_id, roles, sql_names, orm_names FROM "+f.schema+".delivery_audit").Scan(&gotUser, &gotContact, &gotRoles, &sqlNames, &ormNames); err != nil {
		t.Fatal(err)
	}

	if gotUser != userID || gotContact != contactID || gotRoles != roles || sqlNames != names || ormNames != names {
		t.Fatalf("identity/rows = %q %q %q %q %q, want %q %q %q %q", gotUser, gotContact, gotRoles, sqlNames, ormNames, userID, contactID, roles, names)
	}

	var updated string
	if err := f.admin.QueryRowContext(t.Context(), "SELECT COALESCE(string_agg(name, ',' ORDER BY name), '') FROM "+f.schema+".widget WHERE seen = 1").Scan(&updated); err != nil {
		t.Fatal(err)
	}

	if updated != names {
		t.Fatalf("host.db.exec updated %q, want %q", updated, names)
	}
}

func TestEventHandlers_HostCallsUseLiveABACIdentity(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(map[bool]string{false: "sync", true: "async"}[async], func(t *testing.T) {
			f := newEventHostFixture(t)
			f.setPolicy(t, "user_has_role('sales_manager')")
			f.read(t, async, f.userID, f.contactID, "sales_manager", "other,own")

			if err := f.roles.RevokeRole(t.Context(), f.tenant.Slug, f.userID, f.managerID); err != nil {
				t.Fatal(err)
			}
			f.read(t, async, f.userID, f.contactID, "", "")

			f.setPolicy(t, "record.owner_contact_id = current_user.contact_id")
			f.read(t, async, f.userID, f.contactID, "", "own")

			if err := f.roles.SetMemberContact(t.Context(), f.tenant.Slug, f.userID, ""); err != nil {
				t.Fatal(err)
			}
			f.read(t, async, f.userID, "", "", "")
			f.read(t, async, "", "", "", "")
		})
	}
}

func TestEventHandlers_CleanUpEveryOutcome(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(map[bool]string{false: "sync", true: "async"}[async], func(t *testing.T) {
			f := newEventHostFixture(t)

			for _, mode := range []string{"success", "retry", "permanent", "trap"} {
				status, err := f.deliver(t, async, f.userID, mode)

				switch mode {
				case "success":
					if err != nil || status != 0 {
						t.Fatalf("success = %d, %v", status, err)
					}
				case "trap":
					if err == nil {
						t.Fatal("expected trap error")
					}
				default:
					want := int32(1)
					if mode == "permanent" {
						want = 2
					}

					if !async && (err != nil || status != want) {
						t.Fatalf("%s = %d, %v", mode, status, err)
					}

					if async {
						if err == nil {
							t.Fatalf("%s succeeded", mode)
						}

						_, cancelled := errors.AsType[*river.JobCancelError](err)
						if cancelled != (mode == "permanent") {
							t.Fatalf("%s cancellation = %v", mode, cancelled)
						}
					}
				}

				if !f.runtime.TxLimiter().TryAcquire() {
					t.Fatal("event handler leaked transaction permit")
				}
				f.runtime.TxLimiter().Release()

				if f.reader.Stats().InUse != 0 {
					t.Fatal("event handler leaked DB connection")
				}
			}

			f.setPolicy(t, "record.owner_contact_id = current_user.contact_id")
			f.read(t, async, f.userID, f.contactID, "sales_manager", "own")
		})
	}
}

func TestEventHandlers_ActingUserResolutionFailsClosed(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(map[bool]string{false: "sync", true: "async"}[async], func(t *testing.T) {
			f := newEventHostFixture(t)

			for _, userID := range []string{uuid.New().String(), f.userID} {
				if userID == f.userID {
					if _, err := f.admin.ExecContext(t.Context(), "UPDATE "+f.schema+".tenant_members SET status = 'suspended' WHERE user_id = $1", userID); err != nil {
						t.Fatal(err)
					}
				}

				_, err := f.deliver(t, async, userID, "read")
				if !errors.Is(err, role.ErrNotMember) {
					t.Fatalf("missing/suspended user = %v", err)
				}

				if async {
					if _, ok := errors.AsType[*river.JobCancelError](err); !ok {
						t.Fatalf("inactive member is retryable: %v", err)
					}
				}
			}

			if _, err := f.admin.ExecContext(t.Context(), "DROP TABLE "+f.schema+".tenant_members CASCADE"); err != nil {
				t.Fatal(err)
			}

			_, err := f.deliver(t, async, f.userID, "read")
			if err == nil {
				t.Fatal("actor lookup failure succeeded")
			}

			if _, ok := errors.AsType[*river.JobCancelError](err); ok {
				t.Fatalf("DB error is permanent: %v", err)
			}

			var count int
			if err := f.admin.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+f.schema+".delivery_audit").Scan(&count); err != nil {
				t.Fatal(err)
			}

			if count != 0 {
				t.Fatal("rejected event executed host calls")
			}
		})
	}
}
