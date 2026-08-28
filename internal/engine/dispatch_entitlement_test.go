package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	sdkengine "github.com/djangbahevans/goerp/sdk/go/engine"
)

// TestBuildChain_UnentitledModuleRouteReturns403BillingModuleNotAvailable
// proves goerp#441's dispatch-gating check: a route naming a module the
// fixture tenant was never granted a plan entitlement for (unlike
// "widgets", which newChainFixture entitles by default) is rejected with
// 403 billing.module_not_available before reaching the module-ready
// check, even though the module itself is loaded and StatusReady.
func TestBuildChain_UnentitledModuleRouteReturns403BillingModuleNotAvailable(t *testing.T) {
	f := newChainFixture(t)

	loadedModules := map[string]*module.LoadedModule{
		"widgets": f.reg.Snapshot().Modules()["widgets"],
		"premium": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []sdkengine.RouteDeclaration{
				{Method: http.MethodGet, Path: "/items", Auth: "required"},
			},
		},
	}
	if _, err := f.reg.Update(loadedModules); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}

	h := f.chain(nil)
	token := f.issueToken(t)

	req := httptest.NewRequest(http.MethodGet, "/premium/items", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}

	var body routeErrorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "billing.module_not_available" {
		t.Errorf("error.code = %q, want %q", body.Error.Code, "billing.module_not_available")
	}
	if body.Error.Details["module"] != "premium" {
		t.Errorf("details.module = %v, want %q", body.Error.Details["module"], "premium")
	}
	if body.Error.Details["upgrade_url"] != "/settings/billing/upgrade" {
		t.Errorf("details.upgrade_url = %v, want %q", body.Error.Details["upgrade_url"], "/settings/billing/upgrade")
	}
}

// TestBuildChain_EntitledModuleRouteIsUnaffectedByGating proves the
// positive case: a route naming a module the tenant IS entitled to
// ("widgets", granted by newChainFixture) reaches past the entitlement
// gate — same 503 module_unavailable TestBuildChain_ModuleRouteValidJWTReachesDispatchWith503
// already proves, restated here to name the entitlement gate explicitly
// rather than relying on that other test's own doc comment.
func TestBuildChain_EntitledModuleRouteIsUnaffectedByGating(t *testing.T) {
	f := newChainFixture(t)
	h := f.chain(nil)
	token := f.issueToken(t)

	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Fatalf("status = 403, want past the entitlement gate (widgets is entitled); body: %s", w.Body.String())
	}
}
