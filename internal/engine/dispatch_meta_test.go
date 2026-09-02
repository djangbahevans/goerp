package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/recordshares"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	sdkengine "github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"go.opentelemetry.io/otel/trace/noop"
)

// metaChain is f.chain, but wired with a real *Engine (moduleRegistry set)
// and GET /_meta/permissions in builtins — dispatchPermissionsRoute reads
// e.moduleRegistry directly, unlike every other test in this package,
// which only ever reaches the module_unavailable gate against a
// zero-value &Engine{}.
func (f *chainFixture) metaChain() http.Handler {
	generousDefault := route.RateLimitConfig{Requests: 10000, WindowSeconds: 60, Scope: "ip"}
	e := &Engine{moduleRegistry: f.reg}
	builtins := map[string]http.Handler{
		"GET /_meta/permissions": http.HandlerFunc(e.dispatchPermissionsRoute),
	}
	return buildChain(e, f.reg, builtins, nil, f.resolver, f.checker, noop.NewTracerProvider().Tracer("test"), f.cacheClient, generousDefault)
}

// reregisterWidgetsWithFieldSecurity re-registers the fixture's "widgets"
// module — StatusReady this time, with a real model declaring field
// security rules against the fixture's own chainTestPermission/
// chainTestUngrantedPermission (already granted/ungranted to the
// fixture's admin role) — safe per newChainFixture's own permission-index
// bookkeeping, since re-registering a module already-indexed permission
// names leaves their indices unchanged.
func reregisterWidgetsWithFieldSecurity(t *testing.T, f *chainFixture) {
	t.Helper()
	widgetModel := model.ModelDeclaration{
		Name:  "widget",
		Table: "widgets",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "name", Def: model.Text().Required()},
			{Name: "notes", Def: model.Text().Access(model.AccessRead(chainTestPermission))},
			{Name: "secret", Def: model.Text().Access(model.AccessRead(chainTestUngrantedPermission))},
		},
	}
	loadedModules := map[string]*module.LoadedModule{
		"widgets": {
			Status: module.StatusReady,
			Manifest: manifest.Manifest{
				Type:        "standard",
				Permissions: []manifest.Permission{{Name: chainTestPermission}, {Name: chainTestUngrantedPermission}},
			},
			ModelDecls: []model.ModelDeclaration{widgetModel},
			ExplicitRoutes: []sdkengine.RouteDeclaration{
				{Method: http.MethodGet, Path: "/items", Auth: "required", Permissions: []string{chainTestPermission}},
				{Method: http.MethodGet, Path: "/forbidden", Auth: "required", Permissions: []string{chainTestUngrantedPermission}},
			},
		},
	}
	if _, err := f.reg.Update(loadedModules); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
}

// grantModuleEntitlement makes f.tenantID plan-entitled to moduleName, via
// a real plan/plan_entitlement/subscription — the same mechanism
// resolver.LoadEntitlements reads from, so modules_enabled reflects it
// for real rather than a hand-built EntitlementSet.
func grantModuleEntitlement(t *testing.T, f *chainFixture, moduleName string) {
	t.Helper()
	ctx := context.Background()
	billingStore := billing.NewStore(f.conn)
	if err := billingStore.Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}
	plan, err := billingStore.CreatePlan(ctx, fmt.Sprintf("metaplan%d", time.Now().UnixNano()), "Meta Test Plan", nil, nil)
	if err != nil {
		t.Fatalf("CreatePlan() error: %v", err)
	}
	if err := billingStore.UpsertPlanEntitlement(ctx, plan.ID, "module."+moduleName, "true"); err != nil {
		t.Fatalf("UpsertPlanEntitlement() error: %v", err)
	}
	now := time.Now()
	if _, err := billingStore.CreateSubscription(ctx, f.tenantID, plan.ID, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("CreateSubscription() error: %v", err)
	}
}

func TestDispatchPermissionsRoute_FullRoundTrip(t *testing.T) {
	f := newChainFixture(t)
	reregisterWidgetsWithFieldSecurity(t, f)
	grantModuleEntitlement(t, f, "widgets")

	token := f.issueToken(t)
	h := f.metaChain()

	req := httptest.NewRequest(http.MethodGet, "/_meta/permissions", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp metaPermissionsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !slices.Contains(resp.Permissions, chainTestPermission) {
		t.Errorf("permissions = %v, want to contain granted %q", resp.Permissions, chainTestPermission)
	}
	if slices.Contains(resp.Permissions, chainTestUngrantedPermission) {
		t.Errorf("permissions = %v, want NOT to contain ungranted %q", resp.Permissions, chainTestUngrantedPermission)
	}

	fa, ok := resp.FieldAccess["widgets.widget"]
	if !ok {
		t.Fatalf("field_access missing \"widgets.widget\", got %v", resp.FieldAccess)
	}
	if _, ok := fa["name"]; ok {
		t.Error("field_access[\"widgets.widget\"] should not list \"name\" — it has no declared rule")
	}
	if !fa["notes"].Read {
		t.Errorf("notes.read = false, want true — caller holds %q", chainTestPermission)
	}
	if fa["secret"].Read {
		t.Errorf("secret.read = true, want false — caller lacks %q", chainTestUngrantedPermission)
	}

	if !slices.Contains(resp.ModulesEnabled, "widgets") {
		t.Errorf("modules_enabled = %v, want to contain entitled+ready module %q", resp.ModulesEnabled, "widgets")
	}
}

func TestDispatchPermissionsRoute_ModuleNotEntitledIsExcluded(t *testing.T) {
	f := newChainFixture(t)
	reregisterWidgetsWithFieldSecurity(t, f)
	// A second StatusReady module the fixture tenant is never entitled to
	// (newChainFixture only grants "module.widgets") — "widgets" itself
	// can't be used for this assertion since goerp#441's dispatch gating
	// needs newChainFixture's tenant entitled to it for this file's other
	// module-dispatch tests to reach past a 403.
	loadedModules := map[string]*module.LoadedModule{
		"widgets":    f.reg.Snapshot().Modules()["widgets"],
		"unentitled": {Status: module.StatusReady, Manifest: manifest.Manifest{Type: "standard"}},
	}
	if _, err := f.reg.Update(loadedModules); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}

	token := f.issueToken(t)
	h := f.metaChain()

	req := httptest.NewRequest(http.MethodGet, "/_meta/permissions", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp metaPermissionsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if slices.Contains(resp.ModulesEnabled, "unentitled") {
		t.Errorf("modules_enabled = %v, want NOT to contain un-entitled module %q", resp.ModulesEnabled, "unentitled")
	}
	if !slices.Contains(resp.ModulesEnabled, "widgets") {
		t.Errorf("modules_enabled = %v, want to still contain entitled module %q", resp.ModulesEnabled, "widgets")
	}
}

// TestDispatchPermissionsRoute_NoTokenReturns401 is the regression test
// for the bug this route's registration is easy to reintroduce: omitting
// Auth: "required" from its route.RouteManifest leaves it reachable
// unauthenticated. A handler-level unit test can't catch this — only a
// real request through the full middleware chain (routeAuthMiddleware
// included) can.
func TestDispatchPermissionsRoute_NoTokenReturns401(t *testing.T) {
	f := newChainFixture(t)
	h := f.metaChain()

	req := httptest.NewRequest(http.MethodGet, "/_meta/permissions", nil)
	req.Host = f.domain
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "unauthenticated" {
		t.Errorf("error.code = %q, want %q", code, "unauthenticated")
	}
}

// dispatchSharesFixture wires up everything the /_meta/shares handlers
// need — a real Engine (primaryDB + wasmRuntime + moduleRegistry +
// userStore + recordSharesStore, every other field left zero-value, the
// same posture dispatchORMFixture uses for Table-backed dispatch), one
// StatusReady module declaring a .Shareable() widget model with a real
// row, and a recipient user shares get granted to.
type dispatchSharesFixture struct {
	e              *Engine
	slug           string
	tenantID       string
	sharerID       string
	recordID       string
	recipientEmail string
	recipientID    string
}

func newDispatchSharesFixture(t *testing.T, shareOpts ...model.SharePermission) *dispatchSharesFixture {
	t.Helper()
	conn := openDispatchORMTestDB(t)
	ensureRiverJobMigrated(t)
	slug := fmt.Sprintf("dispatchsharestest%d", time.Now().UnixNano())
	createFixtureWidgetsSchema(t, conn, slug)

	recordSharesStore := recordshares.NewStore(conn)
	if err := recordSharesStore.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("recordshares Bootstrap() error: %v", err)
	}

	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}
	recipientEmail := fmt.Sprintf("recipient%d@example.com", time.Now().UnixNano())
	recipientID, err := userStore.FindOrCreateInvited(context.Background(), recipientEmail)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, recipientID) })

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

	// shareOpts == nil (the fixture called with zero variadic args, as
	// opposed to a non-nil-but-empty slice) means "don't declare
	// .Shareable() on this model at all" — TestDispatchSharesCreateRoute_
	// RejectsNotShareableModel needs a model where md.Shareable is
	// actually false, not one declared Shareable() with zero permitted
	// permission levels (a different rejection path).
	modelOpts := []model.ModelOption{}
	if shareOpts != nil {
		modelOpts = append(modelOpts, model.Shareable(shareOpts...))
	}
	widgetDecl := model.Define("widget", modelOpts...).WithStandardFields().
		Field("name", model.Text().Required()).
		Field("code", model.Text())

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:       module.StatusReady,
			Manifest:     manifest.Manifest{Name: "testmodule", Type: "standard"},
			ModelDecls:   []model.ModelDeclaration{*widgetDecl},
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	e := &Engine{
		primaryDB:         conn,
		wasmRuntime:       rt,
		moduleRegistry:    reg,
		userStore:         userStore,
		recordSharesStore: recordSharesStore,
	}

	tenantID := "00000000-0000-0000-0000-000000000001"
	sharerID := "00000000-0000-0000-0000-0000000000aa"
	recordID := "11111111-1111-1111-1111-111111111111"

	schemaName := tenantschema.Name(slug)
	if _, err := conn.Exec(fmt.Sprintf(
		`INSERT INTO %s.widget (id, tenant_id, name) VALUES ($1, $2, $3)`, schemaName,
	), recordID, tenantID, "Widget A"); err != nil {
		t.Fatalf("insert fixture widget row: %v", err)
	}

	return &dispatchSharesFixture{
		e: e, slug: slug, tenantID: tenantID, sharerID: sharerID,
		recordID: recordID, recipientEmail: recipientEmail, recipientID: recipientID,
	}
}

// request builds an httptest request carrying the same tenant/auth
// context values tenantResolutionMiddleware/authMiddleware would have
// already stashed by the time these handlers run — dispatchSharesCreate/
// List/DeleteRoute read model/record_id from the body or query, not from
// a routeResolution, unlike dispatchORMRoute.
func (f *dispatchSharesFixture) request(method, target string, body []byte, pathParams map[string]string) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}

	ctx := withTenantContext(r.Context(), &tenantresolve.TenantContext{
		TenantID: f.tenantID,
		Slug:     f.slug,
	})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: f.sharerID})
	if pathParams != nil {
		ctx = route.WithParams(ctx, pathParams)
	}
	return r.WithContext(ctx)
}

func TestDispatchSharesCreateRoute_Success(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare, model.WriteShare)

	body, _ := json.Marshal(map[string]any{
		"model":      "testmodule.widget",
		"record_id":  f.recordID,
		"user_email": f.recipientEmail,
		"permission": "write",
	})
	w := httptest.NewRecorder()
	f.e.dispatchSharesCreateRoute(w, f.request(http.MethodPost, "/_meta/shares", body, nil))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var resp shareResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Model != "testmodule.widget" || resp.RecordID != f.recordID {
		t.Errorf("resp = %+v, want model/record_id to match request", resp)
	}
	if resp.SharedWithUserID != f.recipientID {
		t.Errorf("shared_with_user_id = %q, want %q", resp.SharedWithUserID, f.recipientID)
	}
	if resp.Permission != "write" {
		t.Errorf("permission = %q, want write", resp.Permission)
	}
	if resp.SharedBy != f.sharerID {
		t.Errorf("shared_by = %q, want %q", resp.SharedBy, f.sharerID)
	}

	shares, err := f.e.recordSharesStore.ListForRecord(context.Background(), f.slug, "testmodule.widget", f.recordID)
	if err != nil {
		t.Fatalf("ListForRecord() error: %v", err)
	}
	if len(shares) != 1 {
		t.Fatalf("record_shares rows = %d, want 1", len(shares))
	}
}

func TestDispatchSharesCreateRoute_RejectsWhenSharerCannotReadRecord(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare, model.WriteShare)

	body, _ := json.Marshal(map[string]any{
		"model":      "testmodule.widget",
		"record_id":  "99999999-9999-9999-9999-999999999999", // no such row
		"user_email": f.recipientEmail,
		"permission": "read",
	})
	w := httptest.NewRecorder()
	f.e.dispatchSharesCreateRoute(w, f.request(http.MethodPost, "/_meta/shares", body, nil))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "permission_denied" {
		t.Errorf("error.code = %q, want permission_denied", code)
	}
}

func TestDispatchSharesCreateRoute_RejectsNotShareableModel(t *testing.T) {
	f := newDispatchSharesFixture(t) // no Shareable() options declared at all — Shareable stays false

	body, _ := json.Marshal(map[string]any{
		"model":      "testmodule.widget",
		"record_id":  f.recordID,
		"user_email": f.recipientEmail,
		"permission": "read",
	})
	w := httptest.NewRecorder()
	f.e.dispatchSharesCreateRoute(w, f.request(http.MethodPost, "/_meta/shares", body, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "not_shareable" {
		t.Errorf("error.code = %q, want not_shareable", code)
	}
}

func TestDispatchSharesCreateRoute_RejectsPermissionNotOffered(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare) // write not offered

	body, _ := json.Marshal(map[string]any{
		"model":      "testmodule.widget",
		"record_id":  f.recordID,
		"user_email": f.recipientEmail,
		"permission": "write",
	})
	w := httptest.NewRecorder()
	f.e.dispatchSharesCreateRoute(w, f.request(http.MethodPost, "/_meta/shares", body, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "not_shareable" {
		t.Errorf("error.code = %q, want not_shareable", code)
	}
}

func TestDispatchSharesCreateRoute_RejectsUnknownRecipient(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare)

	body, _ := json.Marshal(map[string]any{
		"model":      "testmodule.widget",
		"record_id":  f.recordID,
		"user_email": "nobody-else@example.com",
		"permission": "read",
	})
	w := httptest.NewRecorder()
	f.e.dispatchSharesCreateRoute(w, f.request(http.MethodPost, "/_meta/shares", body, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "recipient_not_found" {
		t.Errorf("error.code = %q, want recipient_not_found", code)
	}
}

// TestDispatchSharesCreateRoute_DeniedRecordAccessMasksUnknownRecipient
// guards a real fix (goerp#475 /code-review): the record-access capping
// check must run before the recipient email lookup. Otherwise a caller
// with zero access to any real record could still tell registered from
// unregistered emails apart via recipient_not_found vs. permission_denied
// on any record_id, including a nonexistent one — an email-enumeration
// channel unrelated to the record named in the request. With both the
// record and the recipient invalid, the response must be permission_denied
// (the record check), never recipient_not_found (which would mean the
// recipient lookup ran first).
func TestDispatchSharesCreateRoute_DeniedRecordAccessMasksUnknownRecipient(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare)

	body, _ := json.Marshal(map[string]any{
		"model":      "testmodule.widget",
		"record_id":  "99999999-9999-9999-9999-999999999999", // no such row
		"user_email": "nobody-at-all@example.com",            // not registered either
		"permission": "read",
	})
	w := httptest.NewRecorder()
	f.e.dispatchSharesCreateRoute(w, f.request(http.MethodPost, "/_meta/shares", body, nil))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "permission_denied" {
		t.Errorf("error.code = %q, want permission_denied (not recipient_not_found — record access must be checked first)", code)
	}
}

func TestDispatchSharesListRoute_ReturnsSharesForRecord(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare)
	if _, err := f.e.recordSharesStore.Create(context.Background(), f.slug, "testmodule.widget", f.recordID, f.recipientID, "read", f.sharerID, nil); err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}

	target := "/_meta/shares?model=testmodule.widget&record_id=" + f.recordID
	w := httptest.NewRecorder()
	f.e.dispatchSharesListRoute(w, f.request(http.MethodGet, target, nil, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []shareResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].SharedWithUserID != f.recipientID {
		t.Errorf("data = %+v, want one share to %q", resp.Data, f.recipientID)
	}
}

func TestDispatchSharesDeleteRoute_RevokesShare(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare)
	sh, err := f.e.recordSharesStore.Create(context.Background(), f.slug, "testmodule.widget", f.recordID, f.recipientID, "read", f.sharerID, nil)
	if err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchSharesDeleteRoute(w, f.request(http.MethodDelete, "/_meta/shares/"+sh.ID, nil, map[string]string{"id": sh.ID}))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}

	shares, err := f.e.recordSharesStore.ListForRecord(context.Background(), f.slug, "testmodule.widget", f.recordID)
	if err != nil {
		t.Fatalf("ListForRecord() error: %v", err)
	}
	if len(shares) != 0 {
		t.Errorf("record_shares rows after delete = %d, want 0", len(shares))
	}
}

func TestDispatchSharesDeleteRoute_UnknownIDReturns404(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare)

	w := httptest.NewRecorder()
	f.e.dispatchSharesDeleteRoute(w, f.request(http.MethodDelete, "/_meta/shares/99999999-9999-9999-9999-999999999999", nil, map[string]string{"id": "99999999-9999-9999-9999-999999999999"}))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchSharesListRoute_RejectsWhenCallerCannotReadRecord(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare)

	target := "/_meta/shares?model=testmodule.widget&record_id=99999999-9999-9999-9999-999999999999" // no such row
	w := httptest.NewRecorder()
	f.e.dispatchSharesListRoute(w, f.request(http.MethodGet, target, nil, nil))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "permission_denied" {
		t.Errorf("error.code = %q, want permission_denied", code)
	}
}

func TestDispatchSharesDeleteRoute_RejectsWhenCallerCannotReadRecord(t *testing.T) {
	f := newDispatchSharesFixture(t, model.ReadShare)
	// A share pointing at a record_id that doesn't (or no longer) exist —
	// e.g. the underlying row was deleted after the share was granted.
	sh, err := f.e.recordSharesStore.Create(context.Background(), f.slug, "testmodule.widget", "99999999-9999-9999-9999-999999999999", f.recipientID, "read", f.sharerID, nil)
	if err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchSharesDeleteRoute(w, f.request(http.MethodDelete, "/_meta/shares/"+sh.ID, nil, map[string]string{"id": sh.ID}))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "permission_denied" {
		t.Errorf("error.code = %q, want permission_denied", code)
	}

	shares, err := f.e.recordSharesStore.ListForRecord(context.Background(), f.slug, "testmodule.widget", "99999999-9999-9999-9999-999999999999")
	if err != nil {
		t.Fatalf("ListForRecord() error: %v", err)
	}
	if len(shares) != 1 {
		t.Errorf("record_shares rows after rejected delete = %d, want 1 (unchanged)", len(shares))
	}
}

// TestDispatchSharesCreateRoute_RejectsVirtualBackedModel guards the
// Virtual/Transient backend rejection directly (goerp#475 review) — a
// Virtual model has no Postgres table or RLS policy for .Shareable() to
// widen, so requesting a share on one is a rejection before ever
// attempting the host.orm.read capping check, not a generic query
// failure against a nonexistent table.
func TestDispatchSharesCreateRoute_RejectsVirtualBackedModel(t *testing.T) {
	conn := openDispatchORMTestDB(t)
	ensureRiverJobMigrated(t)

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

	virtualDecl := model.ModelDeclaration{
		Name:       "widget",
		Backend:    model.BackendVirtual,
		Shareable:  true,
		SharePerms: []model.SharePermission{model.ReadShare},
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
		},
	}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:       module.StatusReady,
			Manifest:     manifest.Manifest{Name: "testmodule", Type: "connector"},
			ModelDecls:   []model.ModelDeclaration{virtualDecl},
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	e := &Engine{primaryDB: conn, wasmRuntime: rt, moduleRegistry: reg}
	ctx := withTenantContext(context.Background(), &tenantresolve.TenantContext{TenantID: "00000000-0000-0000-0000-000000000001", Slug: "irrelevant"})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "00000000-0000-0000-0000-0000000000aa"})

	body, _ := json.Marshal(map[string]any{
		"model":      "testmodule.widget",
		"record_id":  "11111111-1111-1111-1111-111111111111",
		"user_email": "someone@example.com",
		"permission": "read",
	})
	req := httptest.NewRequest(http.MethodPost, "/_meta/shares", bytes.NewReader(body)).WithContext(ctx)

	w := httptest.NewRecorder()
	e.dispatchSharesCreateRoute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "not_shareable" {
		t.Errorf("error.code = %q, want not_shareable", code)
	}
}
