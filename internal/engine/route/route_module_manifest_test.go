package route

import (
	"reflect"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
)

func TestRegisterModuleRoutes_PopulatesManifestFromExplicitRoute(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "sales", "domain", []ExplicitRoute{
		{
			Method:       "GET",
			Path:         "/orders/{id}",
			Auth:         "optional",
			Permissions:  []string{"sales:order:read"},
			RateLimit:    &RateLimitConfig{Requests: 100, WindowSeconds: 60, Scope: "user"},
			MaxBodyBytes: 65536,
			RawBody:      true,
			Timeout:      10 * time.Second,
			Streaming:    true,
			Websocket:    false,
			PathParams:   map[string]string{"id": "uuid"},
		},
	})
	if err != nil {
		t.Fatalf("RegisterModuleRoutes: %v", err)
	}

	entry, _, result, _ := table.Lookup("GET", "/sales/orders/1")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}

	want := RouteManifest{
		Auth:         "optional",
		Permissions:  []string{"sales:order:read"},
		RateLimit:    &RateLimitConfig{Requests: 100, WindowSeconds: 60, Scope: "user"},
		MaxBodyBytes: 65536,
		RawBody:      true,
		Timeout:      10 * time.Second,
		Streaming:    true,
		Websocket:    false,
		PathParams:   map[string]string{"id": "uuid"},
	}
	if !reflect.DeepEqual(entry.Manifest, want) {
		t.Fatalf("Manifest = %+v, want %+v", entry.Manifest, want)
	}
}

func TestExplicitRoutesFrom_MapsAllFields(t *testing.T) {
	decls := []engine.RouteDeclaration{
		{
			Method:       "POST",
			Path:         "/orders",
			Auth:         "required",
			Permissions:  []string{"sales:order:write"},
			RateLimit:    &engine.RateLimitDecl{Requests: 10, WindowSeconds: 60, Scope: engine.PerTenant},
			MaxBodyBytes: 1024,
			TimeoutMs:    5000,
			Streaming:    false,
			Websocket:    true,
			RawBody:      true,
			PathParams:   map[string]string{"id": "uuid"},
		},
	}

	got := ExplicitRoutesFrom(decls)
	want := []ExplicitRoute{
		{
			Method:       "POST",
			Path:         "/orders",
			Auth:         "required",
			Permissions:  []string{"sales:order:write"},
			RateLimit:    &RateLimitConfig{Requests: 10, WindowSeconds: 60, Scope: "tenant"},
			MaxBodyBytes: 1024,
			RawBody:      true,
			Timeout:      5 * time.Second,
			Streaming:    false,
			Websocket:    true,
			PathParams:   map[string]string{"id": "uuid"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestExplicitRoutesFrom_NilRateLimitStaysNil(t *testing.T) {
	got := ExplicitRoutesFrom([]engine.RouteDeclaration{{Method: "GET", Path: "/x"}})
	if got[0].RateLimit != nil {
		t.Fatalf("RateLimit = %+v, want nil", got[0].RateLimit)
	}
}

func TestExplicitRoutesFrom_EmptyInput(t *testing.T) {
	got := ExplicitRoutesFrom(nil)
	if len(got) != 0 {
		t.Fatalf("got %d routes, want 0", len(got))
	}
}
