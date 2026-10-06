package adminapi

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type activityHostFixture struct {
	mux       *http.ServeMux
	admin     *sql.DB
	reader    *sql.DB
	runtime   *wasm.Runtime
	roles     *role.Store
	tenant    *tenant.Tenant
	schema    string
	userID    string
	contactID string
	managerID string
}

func newActivityHostFixture(t *testing.T) *activityHostFixture {
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
	slug := "activityhost" + uuid.New().String()[:8]
	tn := createTestTenant(t, tenants, admin, slug)
	schema := tenantschema.Name(slug)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE") })
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
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s.widget (
		id UUID PRIMARY KEY DEFAULT uuidv7(), name TEXT NOT NULL, owner_contact_id UUID,
		tenant_id UUID NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), deleted_at TIMESTAMPTZ, created_by UUID, etag TEXT NOT NULL DEFAULT ''
	); ALTER TABLE %s.widget ENABLE ROW LEVEL SECURITY; ALTER TABLE %s.widget FORCE ROW LEVEL SECURITY`, schema, schema, schema)); err != nil {
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
	if _, err := admin.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+schema+"; GRANT SELECT ON "+schema+".widget TO "+schema); err != nil {
		t.Fatal(err)
	}
	reader, err := db.New("postgres://tenant_" + slug + ":dev@localhost:6432/goerp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	rt, err := wasm.New(&config.Config{
		CompilationCache: wasmtest.SharedCompilationCacheDir(), PoolMaxMemoryByes: 64 << 20,
		Environment: string(config.Production), DBMaxConcurrentTransactions: 1,
	}, reader, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })
	compiled, err := rt.CompileModule(ctx, compileActivityFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })
	pool := rt.NewPool("activityfixture", compiled, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: time.Second})
	t.Cleanup(func() { pool.DrainAndClose(cleanupCtx, 5*time.Second) })
	md := model.Define("widget", model.Table("widget")).WithStandardFields().
		Field("name", model.Text().Required()).Field("owner_contact_id", model.UUID())
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{"activityfixture": {
		Manifest: manifest.Manifest{Name: "activityfixture", Type: "standard"},
		Pool:     pool, Capabilities: abi.CapDBRead | abi.CapDBWrite, Status: module.StatusReady,
		ModelDecls: []model.ModelDeclaration{*md},
	}}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterActivityDispatchRoute(mux, ActivityDispatchDeps{
		Registry: reg, Tenants: tenants, Roles: roles, Runtime: rt,
		Credentials: fakeValidator{token: testWorkflowWorkerToken},
	})
	return &activityHostFixture{mux: mux, admin: admin, reader: reader, runtime: rt, roles: roles,
		tenant: tn, schema: schema, userID: userID, contactID: contactID, managerID: managerID}
}

func (f *activityHostFixture) setPolicy(t *testing.T, condition string) {
	t.Helper()
	expr, err := domain.Parse(condition)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := domain.CompileToRLS(expr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.ExecContext(t.Context(), "DROP POLICY IF EXISTS activity_user ON "+f.schema+".widget; CREATE POLICY activity_user ON "+f.schema+".widget FOR SELECT USING ("+sql+")"); err != nil {
		t.Fatal(err)
	}
}

func (f *activityHostFixture) read(t *testing.T, userID, wantContact, wantRoles string, wantNames []string) {
	t.Helper()
	w := postActivityDispatch(t, f.mux, testWorkflowWorkerToken, map[string]any{
		"module": "activityfixture", "activity": "read_widgets", "payload": map[string]any{},
		"tenant_id": f.tenant.ID, "user_id": userID, "workflow_id": "wf-host", "attempt": 2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("read activity = %d: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			Error  string `json:"error"`
			Output *struct {
				Settings struct {
					UserID    string `json:"user_id"`
					ContactID string `json:"contact_id"`
					Roles     string `json:"roles"`
				} `json:"settings"`
				SQLNames []string `json:"sql_names"`
				ORMNames []string `json:"orm_names"`
			} `json:"output"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Error != "" {
		t.Fatalf("activity error: %s", envelope.Data.Error)
	}
	out := envelope.Data.Output
	if out == nil {
		t.Fatalf("activity omitted its host-call output: %s", w.Body.String())
	}
	if out.Settings.UserID != userID || out.Settings.ContactID != wantContact || out.Settings.Roles != wantRoles {
		t.Errorf("settings = %+v, want user %q, contact %q, roles %q", out.Settings, userID, wantContact, wantRoles)
	}
	for name, rows := range map[string][]string{"host.db": out.SQLNames, "host.orm": out.ORMNames} {
		slices.Sort(rows)
		if !slices.Equal(rows, wantNames) {
			t.Errorf("%s sees %v, want %v", name, rows, wantNames)
		}
	}
}

func TestActivityDispatch_HostCallsUseLiveABACIdentity(t *testing.T) {
	f := newActivityHostFixture(t)
	f.setPolicy(t, "user_has_role('sales_manager')")
	f.read(t, f.userID, f.contactID, "sales_manager", []string{"other", "own"})
	if err := f.roles.RevokeRole(t.Context(), f.tenant.Slug, f.userID, f.managerID); err != nil {
		t.Fatal(err)
	}
	f.read(t, f.userID, f.contactID, "", nil)
	f.setPolicy(t, "record.owner_contact_id = current_user.contact_id")
	f.read(t, f.userID, f.contactID, "", []string{"own"})
	if err := f.roles.SetMemberContact(t.Context(), f.tenant.Slug, f.userID, ""); err != nil {
		t.Fatal(err)
	}
	f.read(t, f.userID, "", "", nil)
	f.read(t, "", "", "", nil)
}

func TestActivityDispatch_ReleasesTransactionsAfterSuccessAndTrap(t *testing.T) {
	f := newActivityHostFixture(t)
	for _, trap := range []bool{false, true} {
		w := postActivityDispatch(t, f.mux, testWorkflowWorkerToken, map[string]any{
			"module": "activityfixture", "activity": "open_transaction", "payload": trap,
			"tenant_id": f.tenant.ID, "user_id": f.userID,
		})
		want := http.StatusOK
		if trap {
			want = http.StatusInternalServerError
		}
		if w.Code != want {
			t.Fatalf("trap %v returned %d: %s", trap, w.Code, w.Body.String())
		}
		env := decodeEnvelope(t, w)
		if trap {
			if env.Error == nil || env.Error.Code != "activity_trapped" {
				t.Fatalf("unexpected trap response: %s", w.Body.String())
			}
		} else {
			data, ok := env.Data.(map[string]any)
			if !ok || data["error"] != nil {
				t.Fatalf("transaction activity failed: %s", w.Body.String())
			}
		}
		if !f.runtime.TxLimiter().TryAcquire() {
			t.Fatal("activity leaked its transaction permit")
		}
		f.runtime.TxLimiter().Release()
		if f.reader.Stats().InUse != 0 {
			t.Fatal("activity leaked a database connection")
		}
	}
	f.setPolicy(t, "record.owner_contact_id = current_user.contact_id")
	f.read(t, f.userID, f.contactID, "sales_manager", []string{"own"})
}

func TestActivityDispatch_ActingUserResolutionFailsClosed(t *testing.T) {
	f := newActivityHostFixture(t)
	for _, userID := range []string{uuid.New().String(), f.userID} {
		if userID == f.userID {
			if _, err := f.admin.ExecContext(t.Context(), "UPDATE "+f.schema+".tenant_members SET status = 'suspended' WHERE user_id = $1", userID); err != nil {
				t.Fatal(err)
			}
		}
		w := postActivityDispatch(t, f.mux, testWorkflowWorkerToken, map[string]any{
			"module": "activityfixture", "activity": "open_transaction", "payload": false,
			"tenant_id": f.tenant.ID, "user_id": userID,
		})
		if w.Code != http.StatusForbidden {
			t.Fatalf("inactive member response = %d: %s", w.Code, w.Body.String())
		}
	}
	if _, err := f.admin.ExecContext(t.Context(), "DROP TABLE "+f.schema+".tenant_members CASCADE"); err != nil {
		t.Fatal(err)
	}
	w := postActivityDispatch(t, f.mux, testWorkflowWorkerToken, map[string]any{
		"module": "activityfixture", "activity": "open_transaction", "payload": false,
		"tenant_id": f.tenant.ID, "user_id": f.userID,
	})
	if w.Code != http.StatusInternalServerError || decodeEnvelope(t, w).Error.Code != "internal" {
		t.Fatalf("lookup failure response = %d: %s", w.Code, w.Body.String())
	}
}
