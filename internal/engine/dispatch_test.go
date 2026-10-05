package engine

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/sdk/go/engine"
)

// composedDispatchHandler composes routeResolutionMiddleware and
// buildDispatchHandler the way buildChain does in production.
func composedDispatchHandler(reg *registry.ModuleRegistry, builtins map[string]http.Handler) http.Handler {
	return routeResolutionMiddleware(reg)((&Engine{}).buildDispatchHandler(builtins))
}

func testDispatchHandler(t *testing.T) http.Handler {
	t.Helper()

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"contacts": {
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{
				{Method: "GET", Path: "/ping"},
			},
		},
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	builtins := map[string]http.Handler{
		"GET /_health": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		}),
		"POST /auth/login": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"expires_in":900}`))
		}),
	}
	return composedDispatchHandler(reg, builtins)
}

func TestDispatchHandler_BuiltinRouteReachesRegisteredHandler(t *testing.T) {
	h := testDispatchHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/_health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != `{"status":"healthy"}` {
		t.Errorf("body = %q, want the built-in handler's own body", w.Body.String())
	}
}

func TestDispatchHandler_AuthLoginRouteReachesRegisteredHandler(t *testing.T) {
	h := testDispatchHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != `{"expires_in":900}` {
		t.Errorf("body = %q, want the built-in handler's own body", w.Body.String())
	}
}

// TestDispatchHandler_PathParamsReachBuiltinHandlerViaContext proves the
// route.WithParams/ParamsFromContext plumbing dispatch.go adds actually
// carries a builtin route's extracted path params (e.g. {id} in
// /admin/users/{id}/mfa/reset) through to the handler — real production
// registerBuiltinRoutes registers that exact template, so this exercises
// the genuine path, not a synthetic one.
func TestDispatchHandler_PathParamsReachBuiltinHandlerViaContext(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	var gotID string
	builtins := map[string]http.Handler{
		"POST /admin/users/{id}/mfa/reset": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotID = route.ParamsFromContext(r.Context())["id"]
			w.WriteHeader(http.StatusOK)
		}),
	}
	h := composedDispatchHandler(reg, builtins)

	req := httptest.NewRequest(http.MethodPost, "/admin/users/target-user-123/mfa/reset", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if gotID != "target-user-123" {
		t.Errorf("params[\"id\"] = %q, want %q", gotID, "target-user-123")
	}
}

func TestRouteResolutionMiddleware_MatchesEscapedPath(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"contacts": {
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{
				{Method: "GET", Path: "/by-email/{email}"},
			},
		},
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	var got *routeResolution
	h := routeResolutionMiddleware(reg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = routeResolutionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name, target, wantEmail string
	}{
		{"encoded slash in parameter", "/contacts/by-email/a%2Bb%2Fc%40d.test", "a+b/c@d.test"},
		{"unencoded parameter", "/contacts/by-email/a+b@d.test", "a+b@d.test"},
		{"encoded literal segment", "/con%74acts/by-email/a%2Fb", "a/b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got = nil
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.target, nil))

			if w.Code != http.StatusOK || got == nil {
				t.Fatalf("status = %d, want the route resolved; body: %s", w.Code, w.Body.String())
			}
			if got.entry.PathTemplate != "/contacts/by-email/{email}" {
				t.Errorf("PathTemplate = %q, want /contacts/by-email/{email}", got.entry.PathTemplate)
			}
			if email := got.pathParams["email"]; email != tc.wantEmail {
				t.Errorf("params[email] = %q, want %q", email, tc.wantEmail)
			}
		})
	}
}

func TestModuleRelativePath(t *testing.T) {
	tests := []struct {
		name, escapedPath, prefix, want string
	}{
		{"keeps parameter escaping", "/contacts/by-email/a%2Bb%2Fc%40d.test", "/contacts", "/by-email/a%2Bb%2Fc%40d.test"},
		{"module root", "/contacts", "/contacts", "/"},
		{"module root with trailing slash", "/contacts/", "/contacts", "/"},
		{"encoded prefix", "/con%74acts/a%2Fb", "/contacts", "/a%2Fb"},
		{"duplicate slashes", "//contacts//orders/1", "/contacts", "/orders/1"},
		{"connector prefix", "/connectors/connector_paystack/hooks/x%2Fy", "/connectors/connector_paystack", "/hooks/x%2Fy"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := moduleRelativePath(tc.escapedPath, tc.prefix); got != tc.want {
				t.Errorf("moduleRelativePath(%q, %q) = %q, want %q", tc.escapedPath, tc.prefix, got, tc.want)
			}
		})
	}
}

func TestModuleRequestHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/contacts", nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("X-Forwarded-For", "203.0.113.1")
	r.Header.Add("X-Forwarded-For", "198.51.100.2")
	r.Header["x-raw-lower"] = []string{"kept"}

	want := map[string][]string{
		"content-type":    {"application/json"},
		"x-forwarded-for": {"203.0.113.1", "198.51.100.2"},
		"x-raw-lower":     {"kept"},
	}
	if got := moduleRequestHeaders(r.Header); !reflect.DeepEqual(got, want) {
		t.Errorf("moduleRequestHeaders() = %v, want %v", got, want)
	}
}

func TestModuleRequestHeaders_DropsCredentials(t *testing.T) {
	h := http.Header{
		"Authorization":       {"Bearer erp_secret"},
		"Proxy-Authorization": {"Basic c2VjcmV0"},
		"Cookie":              {"session=secret"},
		"cookie":              {"raw=secret"},
		"Idempotency-Key":     {"k1"},
	}
	want := map[string][]string{"idempotency-key": {"k1"}}
	if got := moduleRequestHeaders(h); !reflect.DeepEqual(got, want) {
		t.Errorf("moduleRequestHeaders() = %v, want %v", got, want)
	}
}

func TestModuleRequestHeaders_MergesNamesDifferingOnlyInCase(t *testing.T) {
	h := http.Header{"X-Probe": {"canonical"}, "x-probe": {"raw"}}
	got := moduleRequestHeaders(h)["x-probe"]
	slices.Sort(got)
	if !slices.Equal(got, []string{"canonical", "raw"}) {
		t.Errorf(`headers["x-probe"] = %q, want both values`, got)
	}
}

func TestDispatchHandler_UnknownPathReturns404(t *testing.T) {
	h := testDispatchHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	assertRouteErrorCode(t, w, "route_not_found")
}

func TestDispatchHandler_WrongMethodReturns405WithAllowHeader(t *testing.T) {
	h := testDispatchHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/_health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
	if allow := w.Header().Get("Allow"); allow != "GET" {
		t.Errorf("Allow header = %q, want %q", allow, "GET")
	}
	assertRouteErrorCode(t, w, "method_not_allowed")
}

// TestDispatchHandler_ModuleRouteNotReadyReturns503 proves a real module
// route resolves through the same RouteTable as built-ins (it isn't a
// 404), but a module that hasn't reached module.StatusReady (the
// "contacts" fixture module here is left at its zero-value Status) can't
// be dispatched to — neither dispatchORMRoute nor a borrowed WASM instance
// makes sense against a module that never finished loading.
func TestDispatchHandler_ModuleRouteNotReadyReturns503(t *testing.T) {
	h := testDispatchHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/contacts/ping", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	assertRouteErrorCode(t, w, "module_unavailable")
}

func TestDispatchHandler_NilSnapshotReturns503(t *testing.T) {
	reg := &registry.ModuleRegistry{} // Update never called — Snapshot() is nil
	h := composedDispatchHandler(reg, nil)

	req := httptest.NewRequest(http.MethodGet, "/_health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	assertRouteErrorCode(t, w, "not_ready")
}

func assertRouteErrorCode(t *testing.T, w *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var body httperr.Envelope
	if err := json.UnmarshalRead(w.Body, &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Errorf("error.code = %q, want %q", body.Error.Code, wantCode)
	}
}
