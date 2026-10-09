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
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/events/def"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/vmihailenco/msgpack/v5"
)

// Direct dispatch bypasses the permission middleware in these fixtures.
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

func newDispatchWorkflowFixture(t *testing.T, declarations ...model.ModelDeclaration) *dispatchWorkflowFixture {
	t.Helper()
	conn := openDispatchORMTestDB(t)
	ensureRiverJobMigrated(t)
	slug := fmt.Sprintf("dispatchworkflowtest%d", time.Now().UnixNano())
	createFixtureOrdersSchema(t, conn, slug)

	rt, err := wasm.New(&config.Config{
		CompilationCache:            wasmtest.SharedCompilationCacheDir(),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           64 << 20,
		DBMaxConcurrentTransactions: 10,
	}, conn, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	md := orderModelDecl()
	if len(declarations) != 0 {
		md = declarations[0]
	}
	mf := manifest.Manifest{Name: "sales", Type: "standard"}
	for _, field := range md.Fields {
		for _, transition := range field.Def.WorkflowTransitions {
			if ev := transition.Event; ev != nil {
				mf.Emits = append(mf.Emits, manifest.EventDeclaration{Name: ev.Name, Version: ev.Version})
			}
		}
	}

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"sales": {
			Status:       module.StatusReady,
			Manifest:     mf,
			ModelDecls:   []model.ModelDeclaration{md},
			Capabilities: abi.CapDBRead | abi.CapDBWrite | abi.CapEventEmit,
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
		tenantID: uuid.NewV7().String(),
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
	id := f.createOrder(t)

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

type workflowConfirmedPayload struct {
	OrderID string `msgpack:"order_id" record:"id"`
	State   string `msgpack:"state"`
	Name    string `msgpack:"name"`
}

func emittingOrderModel() model.ModelDeclaration {
	md := orderModelDecl()
	event := def.Define[workflowConfirmedPayload]("sales.order.confirmed", def.Version(2))
	for i := range md.Fields {
		if md.Fields[i].Name == "state" {
			md.Fields[i].Def.WorkflowTransitions[0] = md.Fields[i].Def.WorkflowTransitions[0].Emits(event)
		}
	}
	return md
}

func newEmittingWorkflowFixture(t *testing.T, md model.ModelDeclaration) *dispatchWorkflowFixture {
	t.Helper()
	f := newDispatchWorkflowFixture(t, md)
	table := route.New()
	if _, err := route.RegisterModelWorkflowActions(table, "sales", "standard", []model.ModelDeclaration{md}); err != nil {
		t.Fatal(err)
	}
	entry, _, result, _ := table.Lookup(http.MethodPost, "/sales/orders/{id}/confirm")
	if result != route.RouteFound {
		t.Fatal("confirm route not found")
	}
	f.entryConfirm = entry

	return f
}

func (f *dispatchWorkflowFixture) events(t *testing.T, name string) []jobqueue.EventDeliveryArgs {
	t.Helper()
	rows, err := f.e.primaryDB.QueryContext(t.Context(), `SELECT args FROM system.river_job WHERE kind = 'event_delivery' AND args->>'event_name' = $1 AND args->>'tenant_id' = $2`, name, f.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var events []jobqueue.EventDeliveryArgs
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			t.Fatal(err)
		}

		var event jobqueue.EventDeliveryArgs
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	return events
}

func TestDispatchORMRoute_WorkflowTransition_EmitsSelectedPostWriteFields(t *testing.T) {
	f := newEmittingWorkflowFixture(t, emittingOrderModel())
	id := f.createOrder(t)
	schema := tenantschema.Name(f.slug)
	if _, err := f.e.primaryDB.ExecContext(t.Context(), `CREATE FUNCTION `+schema+`.rename_confirmed() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE `+schema+`."order" SET name = 'Confirmed by trigger' WHERE id = NEW.id; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.primaryDB.ExecContext(t.Context(), `CREATE TRIGGER rename_confirmed AFTER UPDATE OF state ON `+schema+`."order" FOR EACH ROW EXECUTE FUNCTION `+schema+`.rename_confirmed()`); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id}))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	events := f.events(t, "sales.order.confirmed")
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	event := events[0]
	if event.EventVersion != 2 || event.EventName != "sales.order.confirmed" {
		t.Fatalf("event = %+v", event)
	}

	var payload map[string]any
	if err := msgpack.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 || payload["order_id"] != id || payload["state"] != "confirmed" || payload["name"] != "Confirmed by trigger" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestDispatchORMRoute_WorkflowTransition_EmitsHonoursFieldSecurity(t *testing.T) {
	md := emittingOrderModel()
	for i := range md.Fields {
		if md.Fields[i].Name == "name" {
			md.Fields[i].Def = md.Fields[i].Def.Access(model.AccessRead(perm.Ref("sales:order:name_read")))
		}
	}
	f := newEmittingWorkflowFixture(t, md)
	id := f.createOrder(t)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id}))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	events := f.events(t, "sales.order.confirmed")
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	var payload map[string]any
	if err := msgpack.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["order_id"] != id || payload["state"] != "confirmed" {
		t.Fatalf("payload = %+v, want only order_id and state", payload)
	}
}

func TestDispatchORMRoute_WorkflowTransition_EmitsRollbackOnCommitFailure(t *testing.T) {
	f := newEmittingWorkflowFixture(t, emittingOrderModel())
	id := f.createOrder(t)
	schema := tenantschema.Name(f.slug)
	if _, err := f.e.primaryDB.ExecContext(t.Context(), `CREATE FUNCTION `+schema+`.reject_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'transition rejected at commit'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.primaryDB.ExecContext(t.Context(), `CREATE CONSTRAINT TRIGGER reject_commit AFTER UPDATE ON `+schema+`."order" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION `+schema+`.reject_commit()`); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id}))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	if events := f.events(t, "sales.order.confirmed"); len(events) != 0 {
		t.Fatalf("events after rollback = %d", len(events))
	}
	if events := f.events(t, "orm.record.updated"); len(events) != 0 {
		t.Fatalf("ORM events after rollback = %d", len(events))
	}

	var state string
	if err := f.e.primaryDB.QueryRowContext(t.Context(), `SELECT state FROM `+schema+`."order" WHERE id = $1`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "draft" {
		t.Fatalf("state = %s, want draft", state)
	}
}

func TestDispatchORMRoute_WorkflowTransition_WithoutEmitsEmitsNoCustomEvent(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	id := f.createOrder(t)

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id}))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if events := f.events(t, "sales.order.confirmed"); len(events) != 0 {
		t.Fatalf("custom events = %d", len(events))
	}
	if events := f.events(t, "orm.record.updated"); len(events) != 1 {
		t.Fatalf("ORM events = %d, want 1", len(events))
	}
}
