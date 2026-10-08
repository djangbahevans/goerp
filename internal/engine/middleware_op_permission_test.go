package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestBuildChainEnableOpsPermissions(t *testing.T) {
	f := newChainFixture(t)
	md := model.Define("item").EnableOps(
		model.List.Requires(perm.Ref(chainTestPermission)),
		model.Create.Requires(perm.Ref(chainTestUngrantedPermission)),
		model.Get,
	)
	if _, err := f.reg.Update(map[string]*module.LoadedModule{
		"widgets": {
			Manifest: manifest.Manifest{
				Name: "widgets",
				Type: "standard",
				Permissions: []manifest.Permission{
					{Name: chainTestPermission},
					{Name: chainTestUngrantedPermission},
				},
			},
			ModelDecls: []model.ModelDeclaration{*md},
		},
	}); err != nil {
		t.Fatal(err)
	}

	token := f.issueToken(t)
	h := f.chain(nil)
	for _, tc := range []struct {
		method string
		path   string
		status int
		code   string
	}{
		{http.MethodGet, "/widgets/items", http.StatusServiceUnavailable, "module_unavailable"},
		{http.MethodPost, "/widgets/items", http.StatusForbidden, "permission_denied"},
		{http.MethodGet, "/widgets/items/123", http.StatusServiceUnavailable, "module_unavailable"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Host = f.domain
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != tc.status || decodeErrorCode(t, w) != tc.code {
				t.Errorf("status = %d, body = %s, want %d %s", w.Code, w.Body.String(), tc.status, tc.code)
			}
		})
	}
}
