package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/sdk/go/engine"
)

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
	}
	return buildDispatchHandler(reg, builtins)
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

// TestDispatchHandler_ModuleRouteReturns501 proves a real module route
// resolves through the same RouteTable as built-ins (it isn't a 404), but
// isn't silently faked as a success — invokeHandler needs a populated
// EngineResponse and an auth/tenant pipeline that don't exist yet.
func TestDispatchHandler_ModuleRouteReturns501(t *testing.T) {
	h := testDispatchHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/contacts/ping", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}
	assertRouteErrorCode(t, w, "dispatch_not_implemented")
}

func TestDispatchHandler_NilSnapshotReturns503(t *testing.T) {
	reg := &registry.ModuleRegistry{} // Update never called — Snapshot() is nil
	h := buildDispatchHandler(reg, nil)

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
	var body routeErrorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Errorf("error.code = %q, want %q", body.Error.Code, wantCode)
	}
}
