package wasm

import (
	"context"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

func TestInvokeHandleConstraint_RoundTripsThroughRealModule(t *testing.T) {
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

	if !inst.HasHandleConstraint() {
		t.Fatal("HasHandleConstraint() = false, want true (computedfixture exports handle_orm_constraint)")
	}

	reqBytes, err := msgpack.Marshal(abiv1.ConstraintRequest{
		Model:  "testmodule.order",
		Phase:  "delete",
		Record: map[string]any{"state": "confirmed"},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	respBytes, err := inst.InvokeHandleConstraint(t.Context(), reqBytes)
	if err != nil {
		t.Fatalf("InvokeHandleConstraint: %v", err)
	}

	var resp abiv1.ConstraintResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Allowed {
		t.Fatal("Allowed = true, want false (state != draft)")
	}
	if resp.Field != "state" {
		t.Errorf("Field = %q, want state", resp.Field)
	}
}

func TestHasHandleConstraint_MissingExport(t *testing.T) {
	inst := newInstanceForTest(t, handleActivityEchoModule)

	if inst.HasHandleConstraint() {
		t.Error("HasHandleConstraint() = true, want false for a module that never exports handle_orm_constraint")
	}
}
