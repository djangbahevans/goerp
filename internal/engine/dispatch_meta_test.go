package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
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
