package wasm

import (
	"context"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

type wirePreviewRequest = abiv1.PreviewRequest

type wirePreviewResponse = abiv1.PreviewResponse

// TestInvokeHandlePreview_RoundTripsThroughRealModule compiles a real Go
// module registering orm.RegisterPreviewHook for "testmodule.priced_order"
// (testdata/computedfixture) and proves InvokeHandlePreview reaches it
// and returns its response — goerp#372's own AC.
func TestInvokeHandlePreview_RoundTripsThroughRealModule(t *testing.T) {
	wasmBytes := compileComputedFixture(t)

	ctx := context.Background()
	rt := newTestRuntime(t, 64<<20)
	compiled, err := rt.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, "computedfixture", compiled, rt.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	t.Cleanup(func() { _ = inst.module.CloseWithExitCode(context.Background(), 0) })

	if !inst.HasHandlePreview() {
		t.Fatal("HasHandlePreview() = false, want true (computedfixture exports handle_orm_preview)")
	}

	reqBytes, err := msgpack.Marshal(wirePreviewRequest{
		Model:    "testmodule.priced_order",
		Record:   map[string]any{"id": "order-1"},
		TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	respBytes, err := inst.InvokeHandlePreview(context.Background(), reqBytes)
	if err != nil {
		t.Fatalf("InvokeHandlePreview: %v", err)
	}

	var resp wirePreviewResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Record["id"] != "order-1" {
		t.Errorf("record[id] = %v, want order-1", resp.Record["id"])
	}
	if resp.Record["price_list_id"] != "list-acme" {
		t.Errorf("record[price_list_id] = %v, want list-acme", resp.Record["price_list_id"])
	}
}

// TestHasHandlePreview_MissingExport reports false (not a panic) for a
// module with no preview hooks declared — it never exports
// handle_orm_preview at all.
func TestHasHandlePreview_MissingExport(t *testing.T) {
	inst := newInstanceForTest(t, handleActivityEchoModule)

	if inst.HasHandlePreview() {
		t.Error("HasHandlePreview() = true, want false for a module that never exports handle_orm_preview")
	}
}
