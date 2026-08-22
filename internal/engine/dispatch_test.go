package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/sdk/go/engine"
)

// composedDispatchHandler wraps buildDispatchHandler with
// routeResolutionMiddleware — the same composition buildChain uses in
// production — since route resolution moved out of buildDispatchHandler
// itself and into that middleware (goerp#330).
func composedDispatchHandler(reg *registry.ModuleRegistry, builtins map[string]http.Handler) http.Handler {
	return routeResolutionMiddleware(reg)(buildDispatchHandler(builtins))
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
	var body routeErrorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Errorf("error.code = %q, want %q", body.Error.Code, wantCode)
	}
}
