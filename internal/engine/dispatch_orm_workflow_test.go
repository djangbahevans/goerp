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
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// orderModelDecl declares a minimal state-machine model — draft ->
// confirmed (gated by a permission the fixture's caller always holds,
// since dispatchORMWorkflowTransition itself never checks Requires; that's
// the standard permission middleware's job, upstream of this handler) and
// confirmed -> cancelled — enough to exercise dispatchORMWorkflowTransition
// (goerp#864) without pulling in the full route.RegisterModelWorkflowActions
// derivation (this fixture builds its RouteEntry by hand, same as
// dispatchORMFixture does for CRUD ops).
func orderModelDecl() model.ModelDeclaration {
	d := model.Define("order").WithStandardFields().
		Field("name", model.Text().Required()).
		Field("state", model.Selection("draft", "confirmed", "cancelled").
			Default("draft").
			Workflow(
				model.Transition("draft", "confirmed", "confirm").Requires("sales:order:confirm"),
				model.Transition("confirmed", "cancelled", "cancel"),
			))
	return *d
}

func createFixtureOrdersSchema(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	})

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.order (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		created_by UUID,
		etag TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'draft'
	)`); err != nil {
		t.Fatalf("create order table: %v", err)
	}
}

type dispatchWorkflowFixture struct {
	e            *Engine
	slug         string
	tenantID     string
	entryCreate  *route.RouteEntry
	entryConfirm *route.RouteEntry
	entryCancel  *route.RouteEntry
}

func newDispatchWorkflowFixture(t *testing.T) *dispatchWorkflowFixture {
	t.Helper()
	conn := openDispatchORMTestDB(t)
	ensureRiverJobMigrated(t)
	slug := fmt.Sprintf("dispatchworkflowtest%d", time.Now().UnixNano())
	createFixtureOrdersSchema(t, conn, slug)

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
		"sales": {
			Status:       module.StatusReady,
			Manifest:     manifest.Manifest{Name: "sales", Type: "standard"},
			ModelDecls:   []model.ModelDeclaration{orderModelDecl()},
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	e := &Engine{primaryDB: conn, wasmRuntime: rt, moduleRegistry: reg}

	transitionEntry := func(actionName, from, to string) *route.RouteEntry {
		return &route.RouteEntry{
			ModuleName:   "sales",
			PathTemplate: "/sales/orders/{id}/" + actionName,
			Manifest: route.RouteManifest{
				Auth:           "required",
				Model:          "sales.order",
				Name:           actionName,
				CrudAction:     "workflow_transition",
				EngineNative:   true,
				StorageBackend: "table",
				Workflow:       &route.WorkflowManifest{Field: "state", From: from, To: to},
			},
		}
	}

	return &dispatchWorkflowFixture{
		e:        e,
		slug:     slug,
		tenantID: "00000000-0000-0000-0000-000000000001",
		entryCreate: &route.RouteEntry{
			ModuleName: "sales", PathTemplate: "/sales/orders",
			Manifest: route.RouteManifest{Auth: "required", Model: "sales.order", CrudAction: "create", EngineNative: true, StorageBackend: "table"},
		},
		entryConfirm: transitionEntry("confirm", "draft", "confirmed"),
		entryCancel:  transitionEntry("cancel", "confirmed", "cancelled"),
	}
}

func (f *dispatchWorkflowFixture) request(method, target string, body []byte, entry *route.RouteEntry, pathParams map[string]string) *http.Request {
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
		Entitlements: tenantresolve.EntitlementSet{Features: map[string]bool{"module.sales": true}},
	})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})
	return r.WithContext(ctx)
}

func (f *dispatchWorkflowFixture) createOrder(t *testing.T, id string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"id": id, "tenant_id": f.tenantID, "name": "Order " + id})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders", body, f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create order status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_WorkflowTransition_ValidTransitionWritesNewState(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	id := "55555555-5555-5555-5555-555555555551"
	f.createOrder(t, id)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id}))

	if w.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if updated["state"] != "confirmed" {
		t.Errorf("state = %v, want confirmed", updated["state"])
	}
}

func TestDispatchORMRoute_WorkflowTransition_WrongCurrentState_409(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	id := "55555555-5555-5555-5555-555555555552"
	f.createOrder(t, id) // starts in "draft"

	// cancel requires "confirmed"; the record is still "draft".
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/cancel", nil, f.entryCancel, map[string]string{"id": id}))

	if w.Code != http.StatusConflict {
		t.Fatalf("cancel status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
	var errBody map[string]map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errBody["error"]["code"] != abi.ErrCodeInvalidTransition {
		t.Errorf("error code = %q, want %q", errBody["error"]["code"], abi.ErrCodeInvalidTransition)
	}
}

func TestDispatchORMRoute_WorkflowTransition_ChainedTransitionsApplyInOrder(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	id := "55555555-5555-5555-5555-555555555553"
	f.createOrder(t, id)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id}))
	if w.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/cancel", nil, f.entryCancel, map[string]string{"id": id}))
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if updated["state"] != "cancelled" {
		t.Errorf("state = %v, want cancelled", updated["state"])
	}
}

func TestDispatchORMRoute_WorkflowTransition_MissingRecord_404(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	missingID := "99999999-9999-9999-9999-999999999999"

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+missingID+"/confirm", nil, f.entryConfirm, map[string]string{"id": missingID}))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_WorkflowTransition_MissingIDPathParam_400(t *testing.T) {
	f := newDispatchWorkflowFixture(t)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders//confirm", nil, f.entryConfirm, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}
