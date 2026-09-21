package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestNewModuleTestHandler_RecoversAPanicInTheChain(t *testing.T) {
	var reg *registry.ModuleRegistry
	h := NewModuleTestHandler(reg, nil, nil)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/anything", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestNewModuleTestHandler_TransientRouteWithoutACacheClientIsNotImplemented(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Name: "testmodule", Type: "standard"},
			ModelDecls: []model.ModelDeclaration{*model.Define("widget", model.Transient(time.Minute)).EnableOps(model.Get)},
		},
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	h := NewModuleTestHandler(reg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/testmodule/widgets/abc", nil)
	ctx := WithTenantContext(req.Context(), &tenantresolve.TenantContext{
		TenantID:     "t",
		Slug:         "t",
		Entitlements: tenantresolve.EntitlementSet{Features: map[string]bool{"module.testmodule": true}},
	})
	ctx = WithAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: "u", TenantID: "t"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req.WithContext(ctx))

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body: %s", w.Code, w.Body.String())
	}
}
