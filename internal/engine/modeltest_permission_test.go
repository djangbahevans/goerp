package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

func TestNewModuleTestHandlerRoutePermissions(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"widgets": {
			Manifest: manifest.Manifest{
				Name: "widgets",
				Type: "standard",
				Permissions: []manifest.Permission{
					{Name: "widgets:item:read"},
					{Name: "widgets:item:write"},
				},
			},
			ExplicitRoutes: []abiv1.RouteDeclaration{
				{Method: "GET", Path: "/items", Auth: "required", Permissions: []string{"widgets:item:read", "widgets:item:write"}},
				{Method: "GET", Path: "/unknown", Auth: "required", Permissions: []string{"widgets:item:unknown"}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var readOnly, both permission.PermissionBitfield
	for _, name := range []string{"widgets:item:read", "widgets:item:write"} {
		idx, ok := reg.Snapshot().PermissionRegistry().Index(name)
		if !ok {
			t.Fatalf("permission %q not registered", name)
		}
		both.Set(idx)
		if name == "widgets:item:read" {
			readOnly.Set(idx)
		}
	}

	h := NewModuleTestHandler(reg, nil, nil)
	for _, tc := range []struct {
		name   string
		path   string
		auth   *authcheck.AuthContext
		status int
	}{
		{"no principal", "/widgets/items", nil, http.StatusUnauthorized},
		{"anonymous", "/widgets/items", &authcheck.AuthContext{}, http.StatusUnauthorized},
		{"no permissions", "/widgets/items", &authcheck.AuthContext{IsAuthenticated: true}, http.StatusForbidden},
		{"partial permissions", "/widgets/items", &authcheck.AuthContext{IsAuthenticated: true, PermissionSet: readOnly}, http.StatusForbidden},
		{"all permissions reach dispatch", "/widgets/items", &authcheck.AuthContext{IsAuthenticated: true, PermissionSet: both}, http.StatusServiceUnavailable},
		{"unknown permission denies", "/widgets/unknown", &authcheck.AuthContext{IsAuthenticated: true, PermissionSet: both}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			ctx := WithTenantContext(req.Context(), &tenantresolve.TenantContext{
				TenantID:     "t",
				Slug:         "t",
				Entitlements: tenantresolve.EntitlementSet{Features: map[string]bool{"module.widgets": true}},
			})
			ctx = WithAuthContext(ctx, tc.auth)
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req.WithContext(ctx))

			if w.Code != tc.status {
				t.Errorf("status = %d, want %d; body = %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
