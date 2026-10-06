package registry

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/route"
)

func TestWebhookIngressRoute(t *testing.T) {
	table := route.New()
	registerBuiltinRoutes(table)

	entry, params, result, _ := table.Lookup("POST", "/_webhooks/connector_paystack/abc123")
	if result != route.RouteFound {
		t.Fatalf("POST lookup result = %v, want RouteFound", result)
	}
	if params["module_name"] != "connector_paystack" || params["token"] != "abc123" {
		t.Errorf("params = %v, want module_name and token", params)
	}
	if !entry.Manifest.EngineBuiltin || !entry.Manifest.OwnRateLimit {
		t.Errorf("manifest = %+v, want an engine builtin that limits itself", entry.Manifest)
	}

	for _, method := range []string{"GET", "PUT", "DELETE", "PATCH"} {
		_, _, result, allowed := table.Lookup(method, "/_webhooks/connector_paystack/abc123")
		if result != route.RouteMethodNotAllowed || len(allowed) != 1 || allowed[0] != "POST" {
			t.Errorf("%s lookup = %v allowing %v, want RouteMethodNotAllowed allowing only POST", method, result, allowed)
		}
	}
}

func TestConnectorAdminRoutesAreBuiltins(t *testing.T) {
	table := route.New()
	registerBuiltinRoutes(table)

	for _, r := range [][2]string{
		{"GET", "/admin/connectors"},
		{"GET", "/admin/connectors/connector_paystack"},
		{"PATCH", "/admin/config"},
		{"POST", "/admin/connectors/connector_paystack/config/webhook_secret/rotate"},
		{"PATCH", "/admin/connectors/connector_paystack/set-primary"},
		{"DELETE", "/admin/connectors/connector_paystack/webhook"},
	} {
		entry, _, result, _ := table.Lookup(r[0], r[1])
		if result != route.RouteFound || !entry.Manifest.EngineBuiltin {
			t.Errorf("%s %s: result %v, want a found engine builtin", r[0], r[1], result)
		}
	}
}
