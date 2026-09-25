package loader

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

// A real module declaring engine.Body/engine.Returns has those type
// descriptions in /_meta/schema's route entries after loading, and a route
// declaring neither has no request_type/response_type.
func TestLoadModule_TypedRoutes_ReachSchemaResponse(t *testing.T) {
	wasmBytes := compileFixture(t, "typedroutesfixture")
	rt := newRealFixtureRuntime(t)

	m := LoadModule(context.Background(), rt, testPoolCfg(), Source{
		Name:          "contacts",
		ManifestBytes: manifestJSON(t, "contacts", wasmBytes, []string{}),
		WasmBytes:     wasmBytes,
	})
	if m.Status == module.StatusFailed {
		t.Fatalf("Status = StatusFailed, FailureReason = %q", m.FailureReason)
	}
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })

	snap, err := (&registry.ModuleRegistry{}).Update(map[string]*module.LoadedModule{"contacts": m})
	if err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	encoded, err := json.Marshal(snap.SchemaResponse())
	if err != nil {
		t.Fatalf("marshal schema response: %v", err)
	}
	var schema struct {
		Modules map[string]struct {
			Routes []struct {
				Method       string         `json:"method"`
				Path         string         `json:"path"`
				Name         string         `json:"name"`
				RequestType  map[string]any `json:"request_type"`
				ResponseType map[string]any `json:"response_type"`
			} `json:"routes"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatalf("decode schema response: %v", err)
	}

	mergeRequest := map[string]any{
		"kind": "object",
		"name": "MergeContactsRequest",
		"fields": []any{
			map[string]any{"name": "target_id", "type": map[string]any{"kind": "string"}},
			map[string]any{"name": "source_ids", "type": map[string]any{"kind": "array", "elem": map[string]any{"kind": "string"}}},
		},
	}
	contact := map[string]any{
		"kind": "object",
		"name": "Contact",
		"fields": []any{
			map[string]any{"name": "id", "type": map[string]any{"kind": "string"}},
			map[string]any{"name": "email", "type": map[string]any{"kind": "string", "nullable": true}},
			map[string]any{"name": "note", "type": map[string]any{"kind": "string"}, "optional": true},
			map[string]any{"name": "updated_at", "type": map[string]any{"kind": "string"}},
		},
	}

	checked := 0
	for _, r := range schema.Modules["contacts"].Routes {
		var wantReq, wantResp any
		switch {
		case r.Method == "POST" && r.Path == "/contacts/export":
			wantReq, wantResp = mergeRequest, map[string]any{"kind": "array", "elem": contact}
		case r.Name == "merge":
			wantReq, wantResp = mergeRequest, contact
		case r.Method == "GET" && r.Path == "/contacts/ping":
			if r.RequestType != nil || r.ResponseType != nil {
				t.Errorf("GET /contacts/ping: request_type = %v, response_type = %v, want both absent", r.RequestType, r.ResponseType)
			}
			checked++
			continue
		default:
			continue
		}
		checked++
		if got := mustJSON(t, r.RequestType); got != mustJSON(t, wantReq) {
			t.Errorf("%s %s request_type = %s, want %s", r.Method, r.Path, got, mustJSON(t, wantReq))
		}
		if got := mustJSON(t, r.ResponseType); got != mustJSON(t, wantResp) {
			t.Errorf("%s %s response_type = %s, want %s", r.Method, r.Path, got, mustJSON(t, wantResp))
		}
	}
	if checked != 3 {
		t.Fatalf("checked %d of the fixture's 3 routes; schema routes: %s", checked, encoded)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
