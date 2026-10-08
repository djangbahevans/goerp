package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
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
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

// The caller holds the transition permission because this fixture bypasses the upstream
// permission middleware.
func orderModelDecl() model.ModelDeclaration {
	d := model.Define("order").WithStandardFields().
		Field("name", model.Text().Required()).
		Field("state", model.Selection("draft", "confirmed", "rejected", "cancelled").
			Default("draft").
			Workflow(
				model.Transition("draft", "confirmed", "confirm").Requires(perm.Ref("sales:order:confirm")),
				model.Transition("draft", "rejected", "reject"),
				model.Transition("confirmed", "cancelled", "cancel"),
				model.Transition("confirmed", "draft", "reopen"),
			))
	return *d
}

func createFixtureOrdersSchema(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := t.Context()
	schemaName := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	})

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.order (
		id UUID PRIMARY KEY DEFAULT uuidv7(),
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
	entryReject  *route.RouteEntry
	entryCancel  *route.RouteEntry
	entryReopen  *route.RouteEntry
}

func newDispatchWorkflowFixture(t *testing.T) *dispatchWorkflowFixture {
	t.Helper()
	conn := openDispatchORMTestDB(t)
	ensureRiverJobMigrated(t)
	slug := fmt.Sprintf("dispatchworkflowtest%d", time.Now().UnixNano())
	createFixtureOrdersSchema(t, conn, slug)

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
		entryReject:  transitionEntry("reject", "draft", "rejected"),
		entryCancel:  transitionEntry("cancel", "confirmed", "cancelled"),
		entryReopen:  transitionEntry("reopen", "confirmed", "draft"),
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

func (f *dispatchWorkflowFixture) createOrder(t *testing.T) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": "Order"})
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders", body, f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create order status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created order has no id")
	}
	return id
}

func TestDispatchORMRoute_WorkflowTransition_ValidTransitionWritesNewState(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	id := f.createOrder(t)

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
	id := f.createOrder(t) // starts in "draft"

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
	if errBody["error"]["code"] != abiv1.ErrCodeInvalidTransition {
		t.Errorf("error code = %q, want %q", errBody["error"]["code"], abiv1.ErrCodeInvalidTransition)
	}
}

func TestDispatchORMRoute_WorkflowTransition_ChainedTransitionsApplyInOrder(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	id := f.createOrder(t)

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

// Concurrent transitions can read the same state before either writes. The etag
// precondition must allow exactly one write and return 409 for the other.
func TestDispatchORMRoute_WorkflowTransition_ConcurrentTransitionsFromSameState_OnlyOneWins(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	id := f.createOrder(t)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id}))
	if w.Code != http.StatusOK {
		t.Fatalf("setup confirm status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var wg sync.WaitGroup
	codes := make([]int, 2)
	bodies := make([]string, 2)

	run := func(i int, entry *route.RouteEntry, action string) {
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/"+action, nil, entry, map[string]string{"id": id}))
		codes[i] = w.Code
		bodies[i] = w.Body.String()
	}

	wg.Go(func() { run(0, f.entryCancel, "cancel") })
	wg.Go(func() { run(1, f.entryReopen, "reopen") })
	wg.Wait()

	var successes, conflicts int
	for i, code := range codes {
		switch code {
		case http.StatusOK:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("request %d: status = %d, want 200 or 409; body: %s", i, code, bodies[i])
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("codes = %v, want exactly one 200 and one 409 (bodies: %v)", codes, bodies)
	}
}

func TestDispatchORMRoute_WorkflowTransition_ConcurrentTransitionsFromCreate_OnlyOneWins(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	id := f.createOrder(t)

	codes := make([]int, 2)
	bodies := make([]string, 2)

	run := func(i int, entry *route.RouteEntry, action string) {
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/"+action, nil, entry, map[string]string{"id": id}))
		codes[i] = w.Code
		bodies[i] = w.Body.String()
	}

	var wg sync.WaitGroup
	wg.Go(func() { run(0, f.entryConfirm, "confirm") })
	wg.Go(func() { run(1, f.entryReject, "reject") })
	wg.Wait()

	var successes, conflicts int
	for i, code := range codes {
		switch code {
		case http.StatusOK:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("request %d: status = %d, want 200 or 409; body: %s", i, code, bodies[i])
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("codes = %v, want exactly one 200 and one 409 (bodies: %v)", codes, bodies)
	}
}

// restrictedOrderModelDecl mirrors orderModelDecl but the gating "state"
// field also carries its own, independent field-read permission — a
// distinct rule from any transition's own .Requires() — to exercise
// dispatchORMWorkflowTransition's internal state read against a caller
// who holds the transition's permission but not the field's.
func restrictedOrderModelDecl() model.ModelDeclaration {
	d := model.Define("order").WithStandardFields().
		Field("name", model.Text().Required()).
		Field("state", model.Selection("draft", "confirmed").
			Default("draft").
			Access(model.AccessRead(perm.Ref("sales:order:state_read"))).
			Workflow(
				model.Transition("draft", "confirmed", "confirm").Requires(perm.Ref("sales:order:confirm")),
			))
	return *d
}

// Internal state checks bypass field masking so a caller authorized for the transition can
// act without permission to read the state field.
func TestDispatchORMRoute_WorkflowTransition_FieldReadPermissionDoesNotBlockStateCheck(t *testing.T) {
	conn := openDispatchORMTestDB(t)
	ensureRiverJobMigrated(t)
	slug := fmt.Sprintf("dispatchworkflowfieldsec%d", time.Now().UnixNano())
	createFixtureOrdersSchema(t, conn, slug)

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
		"sales": {
			Status:       module.StatusReady,
			Manifest:     manifest.Manifest{Name: "sales", Type: "standard"},
			ModelDecls:   []model.ModelDeclaration{restrictedOrderModelDecl()},
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	e := &Engine{primaryDB: conn, wasmRuntime: rt, moduleRegistry: reg}
	tenantID := "00000000-0000-0000-0000-000000000001"

	entryCreate := &route.RouteEntry{
		ModuleName: "sales", PathTemplate: "/sales/orders",
		Manifest: route.RouteManifest{Auth: "required", Model: "sales.order", CrudAction: "create", EngineNative: true, StorageBackend: "table"},
	}
	entryConfirm := &route.RouteEntry{
		ModuleName: "sales", PathTemplate: "/sales/orders/{id}/confirm",
		Manifest: route.RouteManifest{
			Auth: "required", Model: "sales.order", Name: "confirm", CrudAction: "workflow_transition",
			EngineNative: true, StorageBackend: "table",
			Workflow: &route.WorkflowManifest{Field: "state", From: "draft", To: "confirmed"},
		},
	}

	// No PermissionSet at all — this caller holds neither
	// sales:order:state_read nor sales:order:confirm. dispatchORMRoute
	// itself never checks entry.Manifest.Permissions (that's the upstream
	// middleware's job, bypassed by calling dispatchORMRoute directly in
	// this fixture, same as every other test in this file) — what's under
	// test is only whether the internal state read is unmasked.
	req := func(method, target string, body []byte, entry *route.RouteEntry, pathParams map[string]string) *http.Request {
		var r *http.Request
		if body != nil {
			r = httptest.NewRequest(method, target, bytes.NewReader(body))
		} else {
			r = httptest.NewRequest(method, target, nil)
		}
		ctx := withRouteResolution(r.Context(), &routeResolution{snap: e.moduleRegistry.Snapshot(), entry: entry, pathParams: pathParams})
		ctx = withTenantContext(ctx, &tenantresolve.TenantContext{TenantID: tenantID, Slug: slug, Entitlements: tenantresolve.EntitlementSet{Features: map[string]bool{"module.sales": true}}})
		ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})
		return r.WithContext(ctx)
	}

	createBody, _ := json.Marshal(map[string]any{"name": "Order"})
	w := httptest.NewRecorder()
	e.dispatchORMRoute(w, req(http.MethodPost, "/sales/orders", createBody, entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created order has no id")
	}

	w = httptest.NewRecorder()
	e.dispatchORMRoute(w, req(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, entryConfirm, map[string]string{"id": id}))
	if w.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, want 200 (state check must not be masked by the caller's missing field-read permission); body: %s", w.Code, w.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if _, present := updated["state"]; present {
		t.Errorf("response state = %v, want it omitted for a caller without sales:order:state_read", updated["state"])
	}

	var stored string
	if err := conn.QueryRow(`SELECT state FROM `+tenantschema.Name(slug)+`."order" WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read stored state: %v", err)
	}
	if stored != "confirmed" {
		t.Errorf("stored state = %q, want confirmed", stored)
	}
}
