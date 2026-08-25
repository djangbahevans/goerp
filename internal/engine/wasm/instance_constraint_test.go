package wasm

import (
	"context"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

// wireConstraintRequest/wireConstraintResponse mirror sdk/go/orm's own
// unexported constraintRequest/constraintResponse wire shapes by field
// name and msgpack tag — see wireVirtualOpRequest's own doc comment
// (instance_virtualop_test.go) for why this package can't import the
// module-side types directly.
type wireConstraintRequest struct {
	Model  string         `msgpack:"model"`
	Phase  string         `msgpack:"phase"`
	Record map[string]any `msgpack:"record"`
}

type wireConstraintResponse struct {
	Allowed bool   `msgpack:"allowed"`
	Field   string `msgpack:"field,omitempty"`
	Message string `msgpack:"message,omitempty"`
}

// TestInvokeHandleConstraint_RoundTripsThroughRealModule compiles a real
// Go module registering orm.RegisterConstraint for
// ("testmodule.order", orm.OnDelete) (testdata/computedfixture) and
// proves InvokeHandleConstraint reaches it and returns a rejection —
// goerp#378's own AC.
func TestInvokeHandleConstraint_RoundTripsThroughRealModule(t *testing.T) {
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

	if !inst.HasHandleConstraint() {
		t.Fatal("HasHandleConstraint() = false, want true (computedfixture exports handle_orm_constraint)")
	}

	reqBytes, err := msgpack.Marshal(wireConstraintRequest{
		Model:  "testmodule.order",
		Phase:  "delete",
		Record: map[string]any{"state": "confirmed"},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	respBytes, err := inst.InvokeHandleConstraint(context.Background(), reqBytes)
	if err != nil {
		t.Fatalf("InvokeHandleConstraint: %v", err)
	}

	var resp wireConstraintResponse
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

// TestHasHandleConstraint_MissingExport reports false (not a panic) for a
// module with no constraint hooks declared — it never exports
// handle_orm_constraint at all.
func TestHasHandleConstraint_MissingExport(t *testing.T) {
	inst := newInstanceForTest(t, handleActivityEchoModule)

	if inst.HasHandleConstraint() {
		t.Error("HasHandleConstraint() = true, want false for a module that never exports handle_orm_constraint")
	}
}
