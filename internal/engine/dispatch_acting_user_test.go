package engine

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestRequestActingUser_ABAC(t *testing.T) {
	f := newChainFixture(t)
	ctx := t.Context()
	cleanupCtx := context.WithoutCancel(ctx)
	schema := tenantschema.Name(f.tenantSlug)
	createFixtureWidgetsTable(t, f, schema)
	contactID := uuid.New().String()
	roles := role.NewStore(f.conn)
	if err := roles.SetMemberContact(ctx, f.tenantSlug, f.userID, contactID); err != nil {
		t.Fatal(err)
	}
	var managerID string
	if err := f.conn.QueryRowContext(ctx, `INSERT INTO `+schema+`.roles (name) VALUES ('sales_manager') RETURNING id`).Scan(&managerID); err != nil {
		t.Fatal(err)
	}
	if err := roles.AssignRole(ctx, f.tenantSlug, f.userID, managerID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.ExecContext(ctx, `INSERT INTO `+schema+`.widget (tenant_id, name, owner_contact_id)
		VALUES ($1, 'own', $2), ($1, 'other', $3)`, f.tenantID, contactID, uuid.New().String()); err != nil {
		t.Fatal(err)
	}

	// Both host.db's tenant role and host.orm's login must enforce RLS.
	if _, err := f.conn.ExecContext(ctx, "CREATE ROLE "+schema+" LOGIN PASSWORD 'dev' NOSUPERUSER NOBYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.conn.ExecContext(cleanupCtx, "DROP OWNED BY "+schema+"; DROP ROLE "+schema); err != nil {
			t.Errorf("drop fixture role: %v", err)
		}
	})
	if _, err := f.conn.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+schema+"; GRANT SELECT ON "+schema+".widget TO "+schema); err != nil {
		t.Fatal(err)
	}
	reader, err := db.New("postgres://tenant_" + f.tenantSlug + ":dev@localhost:6432/goerp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	path := filepath.Join(t.TempDir(), "actinguser.wasm")
	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", path, "./testdata/actinguserfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build acting user fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := wasm.New(&config.Config{
		CompilationCache: wasmtest.SharedCompilationCacheDir(), Environment: string(config.Production),
		PoolMaxMemoryByes: 64 << 20, DBMaxConcurrentTransactions: 10,
	}, reader, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })
	compiled, err := rt.CompileModule(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })
	pool := rt.NewPool("widgets", compiled, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: 5 * time.Second})
	t.Cleanup(func() { pool.DrainAndClose(cleanupCtx, 5*time.Second) })
	md := model.Define("widget", model.Table("widget")).WithStandardFields().
		Field("name", model.Text().Required()).Field("owner_contact_id", model.UUID()).EnableOps(model.List)
	mod := &module.LoadedModule{
		Status: module.StatusReady, Pool: pool, Capabilities: abi.CapDBRead,
		Manifest:       manifest.Manifest{Name: "widgets", Type: "standard", Permissions: []manifest.Permission{{Name: chainTestPermission}}},
		ModelDecls:     []model.ModelDeclaration{*md},
		ExplicitRoutes: []abiv1.RouteDeclaration{{Method: http.MethodGet, Path: "/sql", Auth: "required"}},
	}
	if _, err := f.reg.Update(map[string]*module.LoadedModule{"widgets": mod}); err != nil {
		t.Fatal(err)
	}
	e := &Engine{primaryDB: reader, wasmRuntime: rt, moduleRegistry: f.reg}
	h := buildChain(e, f.reg, nil, nil, f.resolver, f.checker, noop.NewTracerProvider().Tracer("test"), f.cacheClient,
		route.RateLimitConfig{Requests: 10000, WindowSeconds: 60, Scope: "ip"})
	token := f.issueToken(t)

	read := func(want []string, wantContact, wantRoles string) {
		t.Helper()
		for _, path := range []string{"/widgets/sql", "/widgets/widgets"} {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Host = f.domain
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body.String())
			}
			var out struct {
				Data []struct {
					Name string `json:"name"`
				} `json:"data"`
				Settings []struct {
					ContactID string `json:"contact_id"`
					Roles     string `json:"roles"`
				} `json:"settings"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			names := make([]string, len(out.Data))
			for i, record := range out.Data {
				names[i] = record.Name
			}
			slices.Sort(names)
			if !slices.Equal(names, want) {
				t.Errorf("GET %s = %v, want %v", path, names, want)
			}
			if path == "/widgets/sql" && (len(out.Settings) != 1 || out.Settings[0].ContactID != wantContact || out.Settings[0].Roles != wantRoles) {
				t.Errorf("session settings = %+v, want contact %q and roles %q", out.Settings, wantContact, wantRoles)
			}
		}
	}
	setPolicy := func(condition string) {
		t.Helper()
		expr, err := domain.Parse(condition)
		if err != nil {
			t.Fatal(err)
		}
		sql, err := domain.CompileToRLS(expr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.conn.ExecContext(ctx, "DROP POLICY IF EXISTS acting_user ON "+schema+".widget; CREATE POLICY acting_user ON "+schema+".widget FOR SELECT USING ("+sql+")"); err != nil {
			t.Fatal(err)
		}
	}
	setPolicy("user_has_role('sales_manager')")
	read([]string{"other", "own"}, contactID, "admin,sales_manager")
	if err := roles.RevokeRole(ctx, f.tenantSlug, f.userID, managerID); err != nil {
		t.Fatal(err)
	}
	read(nil, contactID, "admin")
	setPolicy("record.owner_contact_id = current_user.contact_id")
	read([]string{"own"}, contactID, "admin")
	if err := roles.SetMemberContact(ctx, f.tenantSlug, f.userID, ""); err != nil {
		t.Fatal(err)
	}
	read(nil, "", "admin")
}

func createFixtureWidgetsTable(t *testing.T, f *chainFixture, schema string) {
	t.Helper()
	stmt := fmt.Sprintf(`CREATE TABLE %s.widget (
		id UUID PRIMARY KEY DEFAULT uuidv7(), tenant_id UUID NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ, created_by UUID, etag TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL, owner_contact_id UUID
	); ALTER TABLE %s.widget ENABLE ROW LEVEL SECURITY; ALTER TABLE %s.widget FORCE ROW LEVEL SECURITY`, schema, schema, schema)
	if _, err := f.conn.ExecContext(t.Context(), stmt); err != nil {
		t.Fatal(err)
	}
}

func TestReadRecordsAs_UsesReadersContact(t *testing.T) {
	f := newReadersFixture(t)
	f.restrictWidgetsToOwner(t)
	ctx := t.Context()
	contactID := uuid.New().String()
	if err := f.e.roleStore.SetMemberContact(ctx, f.slug, f.callerID, contactID); err != nil {
		t.Fatal(err)
	}
	schema := tenantschema.Name(f.slug)
	if _, err := f.admin.ExecContext(ctx, "ALTER POLICY widget_owner ON "+schema+
		".widget USING (internal_ref IS NULL OR internal_ref = current_setting('app.current_user_contact_id', true))"); err != nil {
		t.Fatal(err)
	}
	recordID := f.insertWidget(t, "Contact's record", &contactID)
	for _, test := range []struct {
		userID string
		want   int
	}{{f.callerID, 1}, {f.memberID, 0}} {
		records, err := f.e.readRecordsAsErr(ctx, &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.slug},
			test.userID, nil, activityTestModel, []string{recordID}, nil)
		if err != nil || len(records) != test.want {
			t.Errorf("reader %s saw %d records, error = %v; want %d", test.userID, len(records), err, test.want)
		}
	}
}
