package registry

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/engine"
)

func TestSchemaCarriesSSEPollInterval(t *testing.T) {
	reg := &ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{"widgets": {
		Manifest:       manifest.Manifest{Name: "widgets", Type: "domain"},
		ExplicitRoutes: []engine.RouteDeclaration{{Method: "GET", Path: "/events", Streaming: true, PollIntervalMs: 1500}},
	}}); err != nil {
		t.Fatal(err)
	}
	routes := reg.Snapshot().SchemaResponse().Modules["widgets"].Routes
	if len(routes) != 1 || !routes[0].Streaming || routes[0].PollIntervalMs != 1500 {
		t.Fatalf("routes=%+v", routes)
	}
	body, err := json.Marshal(routes[0])
	if err != nil || !strings.Contains(string(body), `"poll_interval_ms":1500`) {
		t.Fatalf("body=%s err=%v", body, err)
	}
	body, err = json.Marshal(SchemaRoute{Method: "GET", Path: "/ordinary"})
	if err != nil || strings.Contains(string(body), "poll_interval_ms") {
		t.Fatalf("body=%s err=%v", body, err)
	}
}

func TestSchemaHashChangesWithSSEPollInterval(t *testing.T) {
	reg := &ModuleRegistry{}
	hash := func(interval int) string {
		t.Helper()
		if _, err := reg.Update(map[string]*module.LoadedModule{"widgets": {
			Manifest:       manifest.Manifest{Name: "widgets", Type: "domain"},
			ExplicitRoutes: []engine.RouteDeclaration{{Method: "GET", Path: "/events", Streaming: true, PollIntervalMs: interval}},
		}}); err != nil {
			t.Fatal(err)
		}
		return reg.Snapshot().SchemaHash()
	}
	if hash(500) == hash(1000) {
		t.Fatal("poll interval changed without changing schema hash")
	}
}
