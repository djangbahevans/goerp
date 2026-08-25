package orm

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

func dispatchPreviewAndDecode(t *testing.T, req previewRequest) previewResponse {
	t.Helper()
	data, err := msgpack.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)

	packed := DispatchPreview(ptr, uint32(len(data)))
	respPtr, respLen := uint32(packed>>32), uint32(packed)

	var resp previewResponse
	if err := msgpack.Unmarshal(engine.ReadMem(respPtr, respLen), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestDispatchPreview_RegisteredHook(t *testing.T) {
	RegisterPreviewHook("test.priced_order", func(ctx PreviewContext, draft map[string]any) map[string]any {
		draft["price_list_id"] = "list-" + ctx.TenantID
		return draft
	})

	resp := dispatchPreviewAndDecode(t, previewRequest{
		Model:    "test.priced_order",
		Record:   map[string]any{"customer_id": "c1"},
		TenantID: "acme",
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Record["price_list_id"] != "list-acme" {
		t.Errorf("record[price_list_id] = %v, want list-acme", resp.Record["price_list_id"])
	}
	if resp.Record["customer_id"] != "c1" {
		t.Errorf("record[customer_id] = %v, want c1 (hook should see the draft it was given)", resp.Record["customer_id"])
	}
}

// TestDispatchPreview_UnregisteredModel_PassesDraftThroughUnchanged covers
// the common case go-sdk-reference.md documents: a model with no
// registered hook is not an error, unlike orm.DispatchVirtualOp's
// virtual_op_not_implemented — the draft comes back exactly as given.
func TestDispatchPreview_UnregisteredModel_PassesDraftThroughUnchanged(t *testing.T) {
	resp := dispatchPreviewAndDecode(t, previewRequest{
		Model:  "test.never_registered",
		Record: map[string]any{"a": int64(1)},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Record["a"] != int64(1) {
		t.Errorf("record[a] = %v, want 1 (draft unchanged)", resp.Record["a"])
	}
}
