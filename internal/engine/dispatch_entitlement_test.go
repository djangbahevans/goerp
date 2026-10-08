package engine

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	sdkengine "github.com/djangbahevans/goerp/sdk/go/engine"
)

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

	var body httperr.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "billing.module_not_available" {
		t.Errorf("error.code = %q, want %q", body.Error.Code, "billing.module_not_available")
	}
	details, _ := body.Error.Details.(map[string]any)
	if details["module"] != "premium" {
		t.Errorf("details.module = %v, want %q", details["module"], "premium")
	}
	if details["upgrade_url"] != "/settings/billing/upgrade" {
		t.Errorf("details.upgrade_url = %v, want %q", details["upgrade_url"], "/settings/billing/upgrade")
	}
}

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

func TestBuildChain_TenantDisabledModuleRouteReturns404(t *testing.T) {
	f := newChainFixture(t)
	if err := billing.NewStore(f.conn).SetModuleEnabledForTenant(t.Context(), f.tenantID, "widgets", false, nil); err != nil {
		t.Fatalf("SetModuleEnabledForTenant() error: %v", err)
	}
	if err := f.cacheClient.Delete(t.Context(), tenantresolve.EntitlementCacheKey(f.tenantID)); err != nil {
		t.Fatalf("invalidate entitlement cache: %v", err)
	}

	h := f.chain(nil)
	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+f.issueToken(t))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
	var body httperr.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "route_not_found" || body.Error.Details != nil {
		t.Errorf("error = %q with details %v, want route_not_found and no details", body.Error.Code, body.Error.Details)
	}
}
