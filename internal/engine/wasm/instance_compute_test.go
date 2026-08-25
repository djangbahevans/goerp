package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

// compileComputedFixture compiles testdata/computedfixture — a real
// module built on the actual SDK (orm.RegisterComputed/
// orm.DispatchComputed), not hand-assembled bytecode — to wasip1 WASM,
// the same way compileVirtualOpFixture (instance_virtualop_test.go)
// compiles testdata/virtualopfixture.
func compileComputedFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "computedfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/computedfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/computedfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// wireComputeRequest/wireComputeResponse mirror sdk/go/orm's own
// unexported computeRequest/computeResponse wire shapes by field name and
// msgpack tag — see wireVirtualOpRequest's own doc comment
// (instance_virtualop_test.go) for why this package can't import the
// module-side types directly.
type wireComputeRequest struct {
	FnName string         `msgpack:"fn_name"`
	Record map[string]any `msgpack:"record"`
}

type wireComputeResponse struct {
	Value any `msgpack:"value,omitempty"`
}

// TestInvokeHandleComputed_RoundTripsThroughRealModule compiles a real Go
// module registering orm.RegisterComputed for "_compute_amount_total"
// (testdata/computedfixture) and proves InvokeHandleComputed reaches the
// registered function and returns its computed value — goerp#377's own
// AC, mirroring goerp#373's identical real-module round-trip requirement
// for InvokeHandleVirtualOp.
func TestInvokeHandleComputed_RoundTripsThroughRealModule(t *testing.T) {
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

	reqBytes, err := msgpack.Marshal(wireComputeRequest{
		FnName: "_compute_amount_total",
		Record: map[string]any{"quantity": int8(3), "unit_price": int8(25)},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	respBytes, err := inst.InvokeHandleComputed(context.Background(), reqBytes)
	if err != nil {
		t.Fatalf("InvokeHandleComputed: %v", err)
	}

	var resp wireComputeResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	got, ok := resp.Value.(int64)
	if !ok || got != 75 {
		t.Errorf("Value = %v (%T), want int64(75)", resp.Value, resp.Value)
	}
}

// TestInvokeHandleComputed_MissingExport reports a descriptive error
// (not a panic) for a module with no Computed fields declared — it never
// exports handle_orm_compute at all.
func TestInvokeHandleComputed_MissingExport(t *testing.T) {
	inst := newInstanceForTest(t, handleActivityEchoModule)

	_, err := inst.InvokeHandleComputed(context.Background(), []byte("payload"))
	if err == nil {
		t.Fatal("expected an error for a module missing handle_orm_compute")
	}
}
