package engine

import (
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/vmihailenco/msgpack/v5"
)

// The wire request's members must be encoded at the top level next to the
// engine-only PermissionSet, not nested under the embedded struct's name, and
// a module decoding into the wire request must recover its own members.
func TestEngineRequestEncodesWireRequestInline(t *testing.T) {
	req := EngineRequest{
		ID: "req-1", Method: "GET", TenantSlug: "acme",
		PermissionSet: permission.PermissionBitfield{5},
	}
	b, err := msgpack.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var keys map[string]any
	if err := msgpack.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "method", "tenant_slug", "requested_at", "PermissionSet"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("missing top-level key %q in %v", k, keys)
		}
	}
	if _, nested := keys["Request"]; nested {
		t.Errorf("wire request is nested under %q instead of inlined", "Request")
	}

	var got abiv1.Request
	if err := msgpack.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "req-1" || got.Method != "GET" || got.TenantSlug != "acme" {
		t.Fatalf("decoded wire request = %+v", got)
	}
}
