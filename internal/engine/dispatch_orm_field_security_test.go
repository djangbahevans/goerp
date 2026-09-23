package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

const employeeBankingReadPermission = "hr:employee:banking_read"

func employeeModelDecl() model.ModelDeclaration {
	d := model.Define("employee").WithStandardFields().
		Field("name", model.Text().Required()).
		Field("bank_account", model.Char().
			Access(model.AccessRead(employeeBankingReadPermission)).
			OnDeniedRead(model.Mask("****{last4}")))
	return *d
}

func createFixtureEmployeesSchema(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := t.Context()
	schemaName := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	})

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.employee (
		id UUID PRIMARY KEY DEFAULT uuidv7(),
		tenant_id UUID NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		created_by UUID,
		etag TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		bank_account TEXT
	)`); err != nil {
		t.Fatalf("create employee table: %v", err)
	}
}

type dispatchEmployeeFixture struct {
	e           *Engine
	conn        *sql.DB
	slug        string
	tenantID    string
	entryCreate *route.RouteEntry
	entryUpdate *route.RouteEntry
}

func newDispatchEmployeeFixture(t *testing.T) *dispatchEmployeeFixture {
	t.Helper()
	conn := openDispatchORMTestDB(t)
	ensureRiverJobMigrated(t)
	slug := fmt.Sprintf("dispatchfieldsectest%d", time.Now().UnixNano())
	createFixtureEmployeesSchema(t, conn, slug)

	rt, err := wasm.New(&config.Config{
		CompilationCache:            filepath.Join(t.TempDir(), "cache"),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           1 << 20,
		DBMaxConcurrentTransactions: 10,
	}, conn, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"hr": {
			Status: module.StatusReady,
			Manifest: manifest.Manifest{
				Name:        "hr",
				Type:        "standard",
				Permissions: []manifest.Permission{{Name: employeeBankingReadPermission}},
			},
			ModelDecls:   []model.ModelDeclaration{employeeModelDecl()},
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	manifestFor := func(action string) route.RouteManifest {
		return route.RouteManifest{
			Auth:           "required",
			Model:          "hr.employee",
			CrudAction:     action,
			EngineNative:   true,
			StorageBackend: "table",
		}
	}

	return &dispatchEmployeeFixture{
		e:           &Engine{primaryDB: conn, wasmRuntime: rt, moduleRegistry: reg},
		conn:        conn,
		slug:        slug,
		tenantID:    "00000000-0000-0000-0000-000000000001",
		entryCreate: &route.RouteEntry{ModuleName: "hr", PathTemplate: "/hr/employees", Manifest: manifestFor("create")},
		entryUpdate: &route.RouteEntry{ModuleName: "hr", PathTemplate: "/hr/employees/{id}", Manifest: manifestFor("update")},
	}
}

func (f *dispatchEmployeeFixture) request(t *testing.T, method, target string, body map[string]any, entry *route.RouteEntry, pathParams map[string]string, grantBanking bool) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	r := httptest.NewRequest(method, target, bytes.NewReader(raw))

	snap := f.e.moduleRegistry.Snapshot()
	var permSet permission.PermissionBitfield
	if grantBanking {
		idx, ok := snap.PermissionRegistry().Index(employeeBankingReadPermission)
		if !ok {
			t.Fatalf("permission %s not registered", employeeBankingReadPermission)
		}
		permSet.Set(idx)
	}

	ctx := withRouteResolution(r.Context(), &routeResolution{snap: snap, entry: entry, pathParams: pathParams})
	ctx = withTenantContext(ctx, &tenantresolve.TenantContext{
		TenantID:     f.tenantID,
		Slug:         f.slug,
		Entitlements: tenantresolve.EntitlementSet{Features: map[string]bool{"module.hr": true}},
	})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{
		IsAuthenticated: true,
		UserID:          "00000000-0000-0000-0000-0000000000aa",
		PermissionSet:   permSet,
	})
	return r.WithContext(ctx)
}

func (f *dispatchEmployeeFixture) storedBankAccount(t *testing.T, id string) string {
	t.Helper()
	var bankAccount string
	if err := f.conn.QueryRow("SELECT bank_account FROM "+tenantschema.Name(f.slug)+".employee WHERE id = $1", id).Scan(&bankAccount); err != nil {
		t.Fatalf("read stored bank_account: %v", err)
	}
	return bankAccount
}

func decodeResponseRecord(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return record
}

func TestDispatchORMRoute_Create_MasksReadRestrictedFieldsInResponse(t *testing.T) {
	f := newDispatchEmployeeFixture(t)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(t, http.MethodPost, "/hr/employees", map[string]any{
		"name": "Ada", "bank_account": "1234567890",
	}, f.entryCreate, nil, false))

	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	record := decodeResponseRecord(t, w)
	if record["bank_account"] != "****7890" {
		t.Errorf("response bank_account = %v, want the masked \"****7890\"", record["bank_account"])
	}
	id, _ := record["id"].(string)
	if id == "" {
		t.Fatal("created record has no id")
	}
	if got := f.storedBankAccount(t, id); got != "1234567890" {
		t.Errorf("stored bank_account = %q, want the real value", got)
	}
}

func TestDispatchORMRoute_Update_MasksUntouchedReadRestrictedFieldInResponse(t *testing.T) {
	f := newDispatchEmployeeFixture(t)
	id := "22222222-2222-2222-2222-222222222222"
	if _, err := f.conn.Exec(
		"INSERT INTO "+tenantschema.Name(f.slug)+".employee (id, tenant_id, name, bank_account) VALUES ($1, $2, 'Ada', '1234567890')",
		id, f.tenantID); err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(t, http.MethodPut, "/hr/employees/"+id,
		map[string]any{"name": "Ada Lovelace"}, f.entryUpdate, map[string]string{"id": id}, false))

	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	record := decodeResponseRecord(t, w)
	if record["name"] != "Ada Lovelace" {
		t.Errorf("response name = %v, want the written value", record["name"])
	}
	if record["bank_account"] != "****7890" {
		t.Errorf("response bank_account = %v, want the masked \"****7890\"", record["bank_account"])
	}
	if got := f.storedBankAccount(t, id); got != "1234567890" {
		t.Errorf("stored bank_account = %q, want the real value unchanged", got)
	}
}

func TestDispatchORMRoute_Update_CallerWithReadPermissionReceivesRealValue(t *testing.T) {
	f := newDispatchEmployeeFixture(t)
	id := "33333333-3333-3333-3333-333333333333"
	if _, err := f.conn.Exec(
		"INSERT INTO "+tenantschema.Name(f.slug)+".employee (id, tenant_id, name, bank_account) VALUES ($1, $2, 'Ada', '1234567890')",
		id, f.tenantID); err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(t, http.MethodPut, "/hr/employees/"+id,
		map[string]any{"name": "Ada Lovelace"}, f.entryUpdate, map[string]string{"id": id}, true))

	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := decodeResponseRecord(t, w)["bank_account"]; got != "1234567890" {
		t.Errorf("response bank_account = %v, want the real value for a caller holding the permission", got)
	}
}
