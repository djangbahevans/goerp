package engine

import (
	"net/http"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/route"
)

func TestVerifyBuiltinRouteParity_AllRegisteredPasses(t *testing.T) {
	table := route.New()
	table.Register("GET", "/_health", &route.RouteEntry{PathTemplate: "/_health"})
	table.Register("POST", "/admin/users/{id}/mfa/reset", &route.RouteEntry{PathTemplate: "/admin/users/{id}/mfa/reset"})

	builtins := map[string]http.Handler{
		"GET /_health":                     http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		"POST /admin/users/{id}/mfa/reset": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}

	if err := verifyBuiltinRouteParity(builtins, table); err != nil {
		t.Errorf("verifyBuiltinRouteParity() error = %v, want nil", err)
	}
}

func TestVerifyBuiltinRouteParity_MissingRegistrationErrors(t *testing.T) {
	table := route.New()
	table.Register("GET", "/_health", &route.RouteEntry{PathTemplate: "/_health"})

	builtins := map[string]http.Handler{
		"GET /_health": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		"GET /auth/me": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}

	if err := verifyBuiltinRouteParity(builtins, table); err == nil {
		t.Error("verifyBuiltinRouteParity() error = nil, want an error for the unregistered GET /auth/me")
	}
}
