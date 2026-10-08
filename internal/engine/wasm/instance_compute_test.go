package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
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

func TestInvokeHandleComputed_RoundTripsThroughRealModule(t *testing.T) {
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

	reqBytes, err := msgpack.Marshal(abiv1.ComputeRequest{
		FnName: "_compute_amount_total",
		Record: map[string]any{"quantity": int8(3), "unit_price": int8(25)},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	respBytes, err := inst.InvokeHandleComputed(t.Context(), reqBytes)
	if err != nil {
		t.Fatalf("InvokeHandleComputed: %v", err)
	}

	var resp abiv1.ComputeResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	got, ok := resp.Value.(int64)
	if !ok || got != 75 {
		t.Errorf("Value = %v (%T), want int64(75)", resp.Value, resp.Value)
	}
}

func TestInvokeHandleComputed_MissingExport(t *testing.T) {
	inst := newInstanceForTest(t, handleActivityEchoModule)

	_, err := inst.InvokeHandleComputed(t.Context(), []byte("payload"))
	if err == nil {
		t.Fatal("expected an error for a module missing handle_orm_compute")
	}
}
