package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	sdkengine "github.com/djangbahevans/goerp/sdk/go/engine"
)

func TestValidatePathParams_UUIDKind(t *testing.T) {
	kinds := map[string]string{"id": pathParamKindUUID}

	if _, ok := validatePathParams(kinds, map[string]string{"id": "11111111-1111-1111-1111-111111111111"}); !ok {
		t.Error("valid UUID rejected")
	}
	if _, ok := validatePathParams(kinds, map[string]string{"id": "not-a-uuid"}); ok {
		t.Error("invalid UUID accepted")
	}
}

func TestValidatePathParams_SlugKind(t *testing.T) {
	kinds := map[string]string{"slug": pathParamKindSlug}

	if _, ok := validatePathParams(kinds, map[string]string{"slug": "acme-corp"}); !ok {
		t.Error("valid slug rejected")
	}
	for _, bad := range []string{"Acme-Corp", "-acme", "a", "acme_corp", "acme.corp"} {
		if _, ok := validatePathParams(kinds, map[string]string{"slug": bad}); ok {
			t.Errorf("invalid slug %q accepted", bad)
		}
	}
}

func TestValidatePathParams_IntKind(t *testing.T) {
	kinds := map[string]string{"n": pathParamKindInt}

	if _, ok := validatePathParams(kinds, map[string]string{"n": "42"}); !ok {
		t.Error("valid int rejected")
	}
	if _, ok := validatePathParams(kinds, map[string]string{"n": "not-a-number"}); ok {
		t.Error("invalid int accepted")
	}
}

func TestValidatePathParams_UndeclaredKindPassesThrough(t *testing.T) {
	kinds := map[string]string{"weird": "some-future-kind-this-function-doesnt-know"}
	if _, ok := validatePathParams(kinds, map[string]string{"weird": "anything at all"}); !ok {
		t.Error("an unrecognized declared kind should pass through, not reject")
	}
}

func TestValidatePathParams_ParamWithNoDeclaredKindIsIgnored(t *testing.T) {
	// A param the route extracted but never declared a kind for isn't
	// this function's concern — RouteManifest.PathParams only names the
	// subset a route author chose to constrain.
	if _, ok := validatePathParams(map[string]string{}, map[string]string{"id": "garbage"}); !ok {
		t.Error("a param with no declared kind should never fail validation")
	}
}

func TestValidatePathParams_DeclaredKindButValueMissingIsIgnored(t *testing.T) {
	kinds := map[string]string{"id": pathParamKindUUID}
	if _, ok := validatePathParams(kinds, map[string]string{}); !ok {
		t.Error("a declared kind with no extracted value should never fail validation")
	}
}

func TestDispatchHandler_InvalidPathParamReturns400(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"widgets": {
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []sdkengine.RouteDeclaration{
				{Method: http.MethodGet, Path: "/items/{id}", PathParams: map[string]string{"id": "uuid"}},
			},
		},
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	h := composedDispatchHandler(reg, nil)
	req := httptest.NewRequest(http.MethodGet, "/widgets/items/not-a-uuid", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	assertRouteErrorCode(t, w, "invalid_path_param")
}

func TestDispatchHandler_ValidPathParamReachesDispatch(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"widgets": {
			Manifest: manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []sdkengine.RouteDeclaration{
				{Method: http.MethodGet, Path: "/items/{id}", PathParams: map[string]string{"id": "uuid"}},
			},
		},
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	h := composedDispatchHandler(reg, nil)
	req := httptest.NewRequest(http.MethodGet, "/widgets/items/11111111-1111-1111-1111-111111111111", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Module dispatch itself isn't built yet (goerp#92) — reaching the
	// 501 stub, rather than the 400 the invalid-param test above gets,
	// is what proves a validly-shaped param passed the check.
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body: %s", w.Code, w.Body.String())
	}
	assertRouteErrorCode(t, w, "dispatch_not_implemented")
}

func TestDispatchHandler_TimeoutDefaultsTo30sWhenManifestTimeoutUnset(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	var gotDeadline time.Time
	var hasDeadline bool
	builtins := map[string]http.Handler{
		"GET /_health": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotDeadline, hasDeadline = r.Context().Deadline()
			w.WriteHeader(http.StatusOK)
		}),
	}
	if _, err := reg.Update(map[string]*module.LoadedModule{}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	h := composedDispatchHandler(reg, builtins)
	before := time.Now()
	req := httptest.NewRequest(http.MethodGet, "/_health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	after := time.Now()

	if !hasDeadline {
		t.Fatal("handler's request context has no deadline — expected the default 30s timeout to be applied")
	}
	wantMin := before.Add(defaultHandlerTimeout)
	wantMax := after.Add(defaultHandlerTimeout)
	if gotDeadline.Before(wantMin) || gotDeadline.After(wantMax) {
		t.Errorf("deadline = %v, want within [%v, %v] (now + %v)", gotDeadline, wantMin, wantMax, defaultHandlerTimeout)
	}
}

// TestDispatchHandler_ManifestTimeoutCancelsContext drives
// buildDispatchHandler directly (bypassing routeResolutionMiddleware)
// with a manually-constructed routeResolution declaring a short
// Timeout, and observes that the context handed downstream is actually
// bounded by it. Module dispatch itself 501s immediately today
// (goerp#92 isn't built), so a builtin handler is used to observe the
// context deadline the way a real handler eventually would.
func TestDispatchHandler_ManifestTimeoutCancelsContext(t *testing.T) {
	var canceled bool
	builtins := map[string]http.Handler{
		"GET /widgets/slow": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				canceled = true
			case <-time.After(2 * time.Second):
			}
			w.WriteHeader(http.StatusOK)
		}),
	}

	rr := &routeResolution{entry: &route.RouteEntry{
		PathTemplate: "/widgets/slow",
		Manifest:     route.RouteManifest{EngineNative: true, Timeout: 20 * time.Millisecond},
	}}

	h := buildDispatchHandler(builtins)
	req := httptest.NewRequest(http.MethodGet, "/widgets/slow", nil)
	req = req.WithContext(withRouteResolution(req.Context(), rr))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !canceled {
		t.Error("handler's context was never canceled — expected it to be bounded by the route's 20ms Timeout")
	}
}
