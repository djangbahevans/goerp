package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
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
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/parquet-go/parquet-go"
)

// dispatchORMTestPostgresDSN points directly at the compose.dev.yml
// Postgres instance, same convention internal/engine/wasm's own tests use.
const dispatchORMTestPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

func openDispatchORMTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.New(dispatchORMTestPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", dispatchORMTestPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// Migrate River explicitly because transactional ORM events need its tables; test ordering
// cannot guarantee another fixture has created them.
func ensureRiverJobMigrated(t *testing.T) {
	t.Helper()
	ctx := t.Context()
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
	d := model.Define("testmodule.widget", model.Table("widget")).WithStandardFields().
		Field("name", model.Text().Required()).
		Field("code", model.Text()).
		Field("internal_ref", model.Text().Readonly()).
		Index("idx_widgets_code_unique", model.BTreeIndex("code").Unique())
	return *d
}

func createFixtureWidgetsSchema(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := t.Context()
	schemaName := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	})

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.widget (
		id UUID PRIMARY KEY DEFAULT uuidv7(),
		tenant_id UUID NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		created_by UUID,
		etag TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		code TEXT,
		internal_ref TEXT
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
		CompilationCache:            wasmtest.SharedCompilationCacheDir(),
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
	ctx = withTenantContext(ctx, &tenantresolve.TenantContext{
		TenantID:     f.tenantID,
		Slug:         f.slug,
		Entitlements: tenantresolve.EntitlementSet{Features: map[string]bool{"module.testmodule": true}},
	})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})
	return r.WithContext(ctx)
}

func TestDispatchORMRoute_Create_Then_Get(t *testing.T) {
	f := newDispatchORMFixture(t)

	createBody, _ := json.Marshal(map[string]any{"name": "Widget A", "code": "W-1"})
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
	if etag, _ := created["etag"].(string); etag == "" || fetched["etag"] != etag {
		t.Errorf("etag: created %v, fetched %v, want the same non-empty etag", created["etag"], fetched["etag"])
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

func TestDispatchORMRoute_Create_ReadonlyField_400(t *testing.T) {
	f := newDispatchORMFixture(t)

	body, _ := json.Marshal(map[string]any{"name": "W", "internal_ref": "x"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), abiv1.ErrCodeFieldNotWritable) {
		t.Errorf("body = %s, want error code %s", w.Body.String(), abiv1.ErrCodeFieldNotWritable)
	}
}

func TestDispatchORMRoute_Create_UniqueViolation_409(t *testing.T) {
	f := newDispatchORMFixture(t)

	firstBody, _ := json.Marshal(map[string]any{"name": "Widget A", "code": "DUPLICATE"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", firstBody, f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	secondBody, _ := json.Marshal(map[string]any{"name": "Widget B", "code": "DUPLICATE"})
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", secondBody, f.entryCreate, nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Update_EtagMismatch_409(t *testing.T) {
	f := newDispatchORMFixture(t)

	createBody, _ := json.Marshal(map[string]any{"name": "Widget A"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", createBody, f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created record has no id")
	}

	updateBody, _ := json.Marshal(map[string]any{"name": "Widget A Renamed"})
	r := f.request(http.MethodPut, "/testmodule/widgets/"+id, updateBody, f.entryUpdate, map[string]string{"id": id})
	r.Header.Set("If-Match", "stale-etag")
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
}

// Check deleted_at directly because the fixture omits the RLS policy that hides soft-
// deleted rows in provisioned tenant schemas.
func TestDispatchORMRoute_Delete_SetsDeletedAt(t *testing.T) {
	f := newDispatchORMFixture(t)

	createBody, _ := json.Marshal(map[string]any{"name": "Widget A"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", createBody, f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created record has no id")
	}

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

	for i, code := range []string{"L-1", "L-2"} {
		body, _ := json.Marshal(map[string]any{"name": fmt.Sprintf("Widget %d", i), "code": code})
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

func TestDispatchORMRoute_List_FilterQueryParamFiltersResults(t *testing.T) {
	f := newDispatchORMFixture(t)

	for i, code := range []string{"F-1", "F-2"} {
		body, _ := json.Marshal(map[string]any{"name": fmt.Sprintf("Filtered %d", i), "code": code})
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

func TestDispatchORMRoute_List_UndeclaredFilterFieldReturns400(t *testing.T) {
	f := newDispatchORMFixture(t)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets?filter[nonexistent]=x", nil, f.entryList, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_List_FormatParquet_ReturnsParquetContentType(t *testing.T) {
	f := newDispatchORMFixture(t)

	body, _ := json.Marshal(map[string]any{"name": "Parquet Widget", "code": "PQ-1"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets?format=parquet", nil, f.entryList, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != parquetContentType {
		t.Fatalf("Content-Type = %q, want %q", got, parquetContentType)
	}
	if w.Body.Len() == 0 {
		t.Fatal("empty parquet body")
	}
}

func TestDispatchORMRoute_List_AcceptParquetHeader_ReturnsParquetContentType(t *testing.T) {
	f := newDispatchORMFixture(t)

	w := httptest.NewRecorder()
	r := f.request(http.MethodGet, "/testmodule/widgets", nil, f.entryList, nil)
	r.Header.Set("Accept", parquetContentType)
	f.e.dispatchORMRoute(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != parquetContentType {
		t.Fatalf("Content-Type = %q, want %q", got, parquetContentType)
	}
}

func TestDispatchORMRoute_List_FormatParquet_FilterAppliesIdentically(t *testing.T) {
	f := newDispatchORMFixture(t)

	for i, code := range []string{"PF-1", "PF-2"} {
		body, _ := json.Marshal(map[string]any{"name": fmt.Sprintf("Filtered Parquet %d", i), "code": code})
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))
		if w.Code != http.StatusCreated {
			t.Fatalf("create %d status = %d, want 201; body: %s", i, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets?format=parquet&filter[code]=PF-1", nil, f.entryList, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// No explicit schema: reads the one the file itself embeds (what
	// writeParquet produced), rather than risking a mismatched hand-written
	// one silently projecting rows away.
	reader := parquet.NewReader(bytes.NewReader(w.Body.Bytes()))
	defer reader.Close()

	var rows []map[string]any
	for {
		row := make(map[string]any)
		err := reader.Read(&row)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("reader.Read: %v", err)
			}
			break
		}
		rows = append(rows, row)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1; rows: %#v", len(rows), rows)
	}
	if rows[0]["code"] != "PF-1" {
		t.Errorf("rows[0][code] = %v, want PF-1", rows[0]["code"])
	}
}

// Parquet pagination uses response headers because the raw file has no JSON envelope.
func TestDispatchORMRoute_List_FormatParquet_CursorHeadersPageThroughResults(t *testing.T) {
	f := newDispatchORMFixture(t)

	for i, code := range []string{"PC-1", "PC-2"} {
		body, _ := json.Marshal(map[string]any{"name": fmt.Sprintf("Cursor Parquet %d", i), "code": code})
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))
		if w.Code != http.StatusCreated {
			t.Fatalf("create %d status = %d, want 201; body: %s", i, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets?format=parquet&limit=1&filter[code][in]=PC-1,PC-2", nil, f.entryList, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Has-More"); got != "true" {
		t.Fatalf("first page X-Has-More = %q, want %q", got, "true")
	}
	cursor := w.Header().Get("X-Next-Cursor")
	if cursor == "" {
		t.Fatal("first page X-Next-Cursor is empty, want a cursor")
	}

	// limit=5 here, not 1: the second page's only remaining record (1)
	// comes back short of that limit, which is what actually proves
	// has_more resolves to false — asking with the same limit=1 would come
	// back exactly full again and (correctly, for simple keyset
	// pagination with no lookahead row) still read as "maybe more".
	w = httptest.NewRecorder()
	target := fmt.Sprintf("/testmodule/widgets?format=parquet&limit=5&filter[code][in]=PC-1,PC-2&cursor=%s", cursor)
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, target, nil, f.entryList, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("second page status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Has-More"); got != "false" {
		t.Fatalf("second page X-Has-More = %q, want %q", got, "false")
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

// This fixture has no WASM pool, so preview must discover the absence of hooks without
// borrowing an instance.
func TestDispatchORMRoute_Preview_NoComputedFields_ReturnsDraftUnchanged(t *testing.T) {
	f := newDispatchORMFixture(t)
	entry := &route.RouteEntry{ModuleName: "testmodule", PathTemplate: "/testmodule/widgets/preview", Manifest: route.RouteManifest{
		Model: "testmodule.widget", CrudAction: "preview", EngineNative: true, StorageBackend: "table",
	}}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets/preview", []byte(`{"name":"Draft Widget"}`), entry, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["name"] != "Draft Widget" {
		t.Errorf("name = %v, want %q", body["name"], "Draft Widget")
	}

	conn := openDispatchORMTestDB(t)
	var count int
	if err := conn.QueryRow("SELECT COUNT(*) FROM " + tenantschema.Name(f.slug) + ".widget").Scan(&count); err != nil {
		t.Fatalf("count widgets: %v", err)
	}
	if count != 0 {
		t.Errorf("widget table has %d rows after preview, want 0 (nothing should ever be persisted)", count)
	}
}

func TestDispatchHandler_EngineNativeRouteReachesDispatchORMRoute(t *testing.T) {
	f := newDispatchORMFixture(t)
	h := f.e.buildDispatchHandler(nil)

	createBody, _ := json.Marshal(map[string]any{"name": "Widget A", "code": "W-92"})
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

func TestDispatchHandler_EngineNativeOversizedBodyReturns413(t *testing.T) {
	f := newDispatchORMFixture(t)
	h := f.e.buildDispatchHandler(nil)

	entry := &route.RouteEntry{
		ModuleName:   f.entryCreate.ModuleName,
		PathTemplate: f.entryCreate.PathTemplate,
		Manifest:     f.entryCreate.Manifest,
	}
	entry.Manifest.MaxBodyBytes = 8

	body, _ := json.Marshal(map[string]any{"name": "Widget A"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, f.request(http.MethodPost, "/testmodule/widgets", body, entry, nil))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body: %s", w.Code, w.Body.String())
	}
	assertRouteErrorCode(t, w, "body_too_large")
}

func TestORMErrorStatus_FieldWriteErrors(t *testing.T) {
	tests := []struct {
		code string
		want int
	}{
		{abiv1.ErrCodeFieldWriteDenied, http.StatusForbidden},
		{abiv1.ErrCodeFieldNotWritable, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := ormErrorStatus(tt.code); got != tt.want {
				t.Errorf("ormErrorStatus(%s) = %d, want %d", tt.code, got, tt.want)
			}
		})
	}
}
