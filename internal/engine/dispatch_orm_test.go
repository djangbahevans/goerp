package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dispatchORMTestPostgresDSN points directly at the compose.dev.yml
// Postgres instance, same convention internal/engine/wasm's own tests use.
const dispatchORMTestPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openDispatchORMTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.New(dispatchORMTestPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", dispatchORMTestPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// ensureRiverJobMigrated applies River's own schema migrations
// (jobqueue.Migrate, idempotent and advisory-locked) against the test
// Postgres instance — ORMCreate/Write/Unlink insert into river_job
// transactionally via the event insert client (host_orm_write.go's
// emitRecordEvent), and nothing guarantees some other package's test has
// already migrated it first; relying on that incidental ordering is
// exactly the kind of flake CI catches (test file execution order within
// a package is filename-alphabetical, and dispatch_orm_test.go sorts
// before whatever file's test happens to construct a full Engine).
func ensureRiverJobMigrated(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dispatchORMTestPostgresDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()
	if err := jobqueue.Migrate(ctx, pool); err != nil {
		t.Fatalf("jobqueue.Migrate: %v", err)
	}
}

func widgetModelDecl() model.ModelDeclaration {
	d := model.Define("widget").WithStandardFields().
		Field("name", model.Text().Required()).
		Field("code", model.Text()).
		Index("idx_widgets_code_unique", model.BTreeIndex("code").Unique())
	return *d
}

func createFixtureWidgetsSchema(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	})

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.widget (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		created_by UUID,
		etag TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		code TEXT
	)`); err != nil {
		t.Fatalf("create widget table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CREATE UNIQUE INDEX idx_widgets_code_unique ON `+schemaName+`.widget (code)`); err != nil {
		t.Fatalf("create unique index: %v", err)
	}
}

// dispatchORMFixture wires up everything dispatchORMRoute needs: a real
// Engine (primaryDB + wasmRuntime + moduleRegistry, every other field left
// zero-value since Table-backed dispatch never touches them), one
// StatusReady module declaring the widget model, and a tenant/auth
// context matching what tenantResolutionMiddleware/authMiddleware would
// have already stashed by the time dispatchORMRoute runs.
type dispatchORMFixture struct {
	e           *Engine
	slug        string
	tenantID    string
	entryList   *route.RouteEntry
	entryGet    *route.RouteEntry
	entryCreate *route.RouteEntry
	entryUpdate *route.RouteEntry
	entryDelete *route.RouteEntry
}

func newDispatchORMFixture(t *testing.T) *dispatchORMFixture {
	t.Helper()
	conn := openDispatchORMTestDB(t)
	ensureRiverJobMigrated(t)
	slug := fmt.Sprintf("dispatchormtest%d", time.Now().UnixNano())
	createFixtureWidgetsSchema(t, conn, slug)

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
		"testmodule": {
			Status:       module.StatusReady,
			Manifest:     manifest.Manifest{Name: "testmodule", Type: "standard"},
			ModelDecls:   []model.ModelDeclaration{widgetModelDecl()},
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	e := &Engine{primaryDB: conn, wasmRuntime: rt, moduleRegistry: reg}

	manifestFor := func(action string) route.RouteManifest {
		return route.RouteManifest{
			Auth:           "required",
			Model:          "testmodule.widget",
			ResponseIsList: action == "list",
			CrudAction:     action,
			EngineNative:   true,
			StorageBackend: "table",
		}
	}

	return &dispatchORMFixture{
		e:           e,
		slug:        slug,
		tenantID:    "00000000-0000-0000-0000-000000000001",
		entryList:   &route.RouteEntry{ModuleName: "testmodule", PathTemplate: "/testmodule/widgets", Manifest: manifestFor("list")},
		entryGet:    &route.RouteEntry{ModuleName: "testmodule", PathTemplate: "/testmodule/widgets/{id}", Manifest: manifestFor("get")},
		entryCreate: &route.RouteEntry{ModuleName: "testmodule", PathTemplate: "/testmodule/widgets", Manifest: manifestFor("create")},
		entryUpdate: &route.RouteEntry{ModuleName: "testmodule", PathTemplate: "/testmodule/widgets/{id}", Manifest: manifestFor("update")},
		entryDelete: &route.RouteEntry{ModuleName: "testmodule", PathTemplate: "/testmodule/widgets/{id}", Manifest: manifestFor("delete")},
	}
}

// request builds an httptest request carrying the same context values
// routeResolutionMiddleware/tenantResolutionMiddleware/authMiddleware
// would have stashed by the time dispatchORMRoute runs.
func (f *dispatchORMFixture) request(method, target string, body []byte, entry *route.RouteEntry, pathParams map[string]string) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}

	ctx := withRouteResolution(r.Context(), &routeResolution{
		snap:       f.e.moduleRegistry.Snapshot(),
		entry:      entry,
		pathParams: pathParams,
	})
	ctx = withTenantContext(ctx, &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.slug})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})
	return r.WithContext(ctx)
}

func TestDispatchORMRoute_Create_Then_Get(t *testing.T) {
	f := newDispatchORMFixture(t)

	createBody, _ := json.Marshal(map[string]any{"id": "11111111-1111-1111-1111-111111111111", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget A", "code": "W-1"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", createBody, f.entryCreate, nil))

	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created["name"] != "Widget A" {
		t.Errorf("created[name] = %v, want Widget A", created["name"])
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created record has no id")
	}

	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets/"+id, nil, f.entryGet, map[string]string{"id": id}))
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var fetched map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if fetched["id"] != id {
		t.Errorf("fetched[id] = %v, want %v", fetched["id"], id)
	}
}

func TestDispatchORMRoute_Get_MissingRecord_404(t *testing.T) {
	f := newDispatchORMFixture(t)

	w := httptest.NewRecorder()
	missingID := "99999999-9999-9999-9999-999999999999"
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets/"+missingID, nil, f.entryGet, map[string]string{"id": missingID}))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Get_MissingIDPathParam_400(t *testing.T) {
	f := newDispatchORMFixture(t)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets/", nil, f.entryGet, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Create_MissingRequiredField_400(t *testing.T) {
	f := newDispatchORMFixture(t)

	body, _ := json.Marshal(map[string]any{"code": "W-2"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Create_UniqueViolation_409(t *testing.T) {
	f := newDispatchORMFixture(t)

	firstBody, _ := json.Marshal(map[string]any{"id": "22222222-2222-2222-2222-222222222222", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget A", "code": "DUPLICATE"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", firstBody, f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	secondBody, _ := json.Marshal(map[string]any{"id": "33333333-3333-3333-3333-333333333333", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget B", "code": "DUPLICATE"})
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", secondBody, f.entryCreate, nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Update_EtagMismatch_409(t *testing.T) {
	f := newDispatchORMFixture(t)

	createBody, _ := json.Marshal(map[string]any{"id": "44444444-4444-4444-4444-444444444444", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget A"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", createBody, f.entryCreate, nil))
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id := created["id"].(string)

	updateBody, _ := json.Marshal(map[string]any{"name": "Widget A Renamed"})
	r := f.request(http.MethodPut, "/testmodule/widgets/"+id, updateBody, f.entryUpdate, map[string]string{"id": id})
	r.Header.Set("If-Match", "stale-etag")
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

// TestDispatchORMRoute_Delete_SetsDeletedAt verifies the row is soft-
// deleted (WithStandardFields declares deleted_at). It checks deleted_at
// directly via SQL rather than a get-after-delete 404, matching
// TestHostORM_Unlink_SoftDeletesWithStandardFields's own precedent
// (internal/engine/wasm/host_orm_write_test.go) — deleted_at IS NULL
// filtering is enforced by the compiled RLS policy a real tenant schema
// gets at provisioning time, which this test's bare hand-written schema
// deliberately doesn't set up (out of scope for exercising dispatchORMRoute
// itself), so ORMRead has no reason to exclude the row here.
func TestDispatchORMRoute_Delete_SetsDeletedAt(t *testing.T) {
	f := newDispatchORMFixture(t)

	createBody, _ := json.Marshal(map[string]any{"id": "55555555-5555-5555-5555-555555555555", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget A"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", createBody, f.entryCreate, nil))
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id := created["id"].(string)

	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodDelete, "/testmodule/widgets/"+id, nil, f.entryDelete, map[string]string{"id": id}))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body: %s", w.Code, w.Body.String())
	}

	var deletedAt sql.NullTime
	schemaName := tenantschema.Name(f.slug)
	if err := f.e.primaryDB.QueryRow(`SELECT deleted_at FROM `+schemaName+`.widget WHERE id = $1`, id).Scan(&deletedAt); err != nil {
		t.Fatalf("query row directly: %v", err)
	}
	if !deletedAt.Valid {
		t.Error("expected deleted_at to be set (soft delete), row was hard-deleted or untouched")
	}
}

func TestDispatchORMRoute_List_ReturnsEnvelope(t *testing.T) {
	f := newDispatchORMFixture(t)

	ids := []string{"66666666-6666-6666-6666-666666666666", "77777777-7777-7777-7777-777777777777"}
	for i, code := range []string{"L-1", "L-2"} {
		body, _ := json.Marshal(map[string]any{"id": ids[i], "tenant_id": "00000000-0000-0000-0000-000000000001", "name": fmt.Sprintf("Widget %d", i), "code": code})
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))
		if w.Code != http.StatusCreated {
			t.Fatalf("create %d status = %d, want 201; body: %s", i, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets", nil, f.entryList, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Cursor  string `json:"cursor"`
			HasMore bool   `json:"has_more"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(envelope.Data) != 2 {
		t.Errorf("len(data) = %d, want 2", len(envelope.Data))
	}
}

// TestDispatchORMRoute_List_FilterQueryParamFiltersResults exercises
// goerp#374's list filter compiler end to end through dispatchORMRoute:
// ?filter[code]=L-1 (the query string goerp#346's List branch reads)
// should return only the matching record, not the unfiltered set.
func TestDispatchORMRoute_List_FilterQueryParamFiltersResults(t *testing.T) {
	f := newDispatchORMFixture(t)

	ids := []string{"88888888-8888-8888-8888-888888888888", "99999999-9999-9999-9999-999999999999"}
	for i, code := range []string{"F-1", "F-2"} {
		body, _ := json.Marshal(map[string]any{"id": ids[i], "tenant_id": "00000000-0000-0000-0000-000000000001", "name": fmt.Sprintf("Filtered %d", i), "code": code})
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))
		if w.Code != http.StatusCreated {
			t.Fatalf("create %d status = %d, want 201; body: %s", i, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets?filter[code]=F-1", nil, f.entryList, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(envelope.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(envelope.Data))
	}
	if envelope.Data[0]["code"] != "F-1" {
		t.Errorf("data[0][code] = %v, want F-1", envelope.Data[0]["code"])
	}
}

// TestDispatchORMRoute_List_UndeclaredFilterFieldReturns400 proves an
// unknown filter[...] field is a descriptive error, not a silently-dropped
// filter (goerp#374's own AC).
func TestDispatchORMRoute_List_UndeclaredFilterFieldReturns400(t *testing.T) {
	f := newDispatchORMFixture(t)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets?filter[nonexistent]=x", nil, f.entryList, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_VirtualBackend_NotImplemented(t *testing.T) {
	f := newDispatchORMFixture(t)
	entry := &route.RouteEntry{ModuleName: "testmodule", PathTemplate: "/testmodule/widgets", Manifest: route.RouteManifest{
		Model: "testmodule.widget", CrudAction: "get", EngineNative: true, StorageBackend: "virtual",
	}}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets/x", nil, entry, map[string]string{"id": "x"}))

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Preview_NotImplemented(t *testing.T) {
	f := newDispatchORMFixture(t)
	entry := &route.RouteEntry{ModuleName: "testmodule", PathTemplate: "/testmodule/widgets/preview", Manifest: route.RouteManifest{
		Model: "testmodule.widget", CrudAction: "preview", EngineNative: true, StorageBackend: "table",
	}}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets/preview", []byte("{}"), entry, nil))

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body: %s", w.Code, w.Body.String())
	}
}

// TestDispatchHandler_EngineNativeRouteReachesDispatchORMRoute drives the
// same create-then-get flow as TestDispatchORMRoute_Create_Then_Get, but
// through buildDispatchHandler (goerp#92) rather than calling
// dispatchORMRoute directly — proving the module_unavailable gate lets a
// StatusReady module through, and that dispatchORMRoute's response
// survives the engineResponseRecorder -> writeResponse round trip
// unchanged (status, body, and the Content-Type header it sets itself).
func TestDispatchHandler_EngineNativeRouteReachesDispatchORMRoute(t *testing.T) {
	f := newDispatchORMFixture(t)
	h := f.e.buildDispatchHandler(nil)

	createBody, _ := json.Marshal(map[string]any{"id": "55555555-5555-5555-5555-555555555555", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget A", "code": "W-92"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, f.request(http.MethodPost, "/testmodule/widgets", createBody, f.entryCreate, nil))

	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created record has no id")
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, f.request(http.MethodGet, "/testmodule/widgets/"+id, nil, f.entryGet, map[string]string{"id": id}))
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var fetched map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if fetched["id"] != id {
		t.Errorf("fetched[id] = %v, want %v", fetched["id"], id)
	}
}

// TestDispatchHandler_EngineNativeOversizedBodyReturns413 proves
// buildDispatchHandler enforces RouteManifest.MaxBodyBytes on an
// EngineNative route (dispatchORMCreate never gets the chance to run) —
// the AC's "rejected outright, not silently truncated" requirement.
func TestDispatchHandler_EngineNativeOversizedBodyReturns413(t *testing.T) {
	f := newDispatchORMFixture(t)
	h := f.e.buildDispatchHandler(nil)

	entry := &route.RouteEntry{
		ModuleName:   f.entryCreate.ModuleName,
		PathTemplate: f.entryCreate.PathTemplate,
		Manifest:     f.entryCreate.Manifest,
	}
	entry.Manifest.MaxBodyBytes = 8

	body, _ := json.Marshal(map[string]any{"id": "66666666-6666-6666-6666-666666666666", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "Widget A"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, f.request(http.MethodPost, "/testmodule/widgets", body, entry, nil))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
	assertRouteErrorCode(t, w, "body_too_large")
}
