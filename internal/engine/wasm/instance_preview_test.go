package wasm

import (
	"context"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

func TestInvokeHandlePreview_RoundTripsThroughRealModule(t *testing.T) {
	wasmBytes := compileComputedFixture(t)

	ctx := t.Context()
	rt := newTestRuntime(t, 64<<20)
	compiled, err := rt.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := newModuleInstance(ctx, "computedfixture", compiled, rt)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	t.Cleanup(func() { _ = inst.module.CloseWithExitCode(context.Background(), 0) })

	if !inst.HasHandlePreview() {
		t.Fatal("HasHandlePreview() = false, want true (computedfixture exports handle_orm_preview)")
	}

	reqBytes, err := msgpack.Marshal(abiv1.PreviewRequest{
		Model:    "testmodule.priced_order",
		Record:   map[string]any{"id": "order-1"},
		TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	respBytes, err := inst.InvokeHandlePreview(t.Context(), reqBytes)
	if err != nil {
		t.Fatalf("InvokeHandlePreview: %v", err)
	}

	var resp abiv1.PreviewResponse
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

func TestHasHandlePreview_MissingExport(t *testing.T) {
	inst := newInstanceForTest(t, handleActivityEchoModule)

	if inst.HasHandlePreview() {
		t.Error("HasHandlePreview() = true, want false for a module that never exports handle_orm_preview")
	}
}
