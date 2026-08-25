package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

// handleVirtualOpEchoModule is handleActivityEchoModule (instance_activity_test.go)
// with its third export renamed from "handle_activity" to "handle_virtual_op"
// — same allocate/deallocate/echo function bodies, exercising
// InvokeHandleVirtualOp's marshal/call/unmarshal round trip without a real
// module.
var handleVirtualOpEchoModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x11, 0x03, 0x60,
	0x01, 0x7F, 0x01, 0x7F, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x60, 0x02, 0x7F,
	0x7F, 0x01, 0x7E, 0x03, 0x04, 0x03, 0x00, 0x01, 0x02, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B, 0x07,
	0x2D, 0x03, 0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00,
	0x00, 0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65,
	0x00, 0x01, 0x11, 0x68, 0x61, 0x6E, 0x64, 0x6C, 0x65, 0x5F, 0x76, 0x69,
	0x72, 0x74, 0x75, 0x61, 0x6C, 0x5F, 0x6F, 0x70, 0x00, 0x02, 0x0A, 0x23,
	0x03, 0x11, 0x01, 0x01, 0x7F, 0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20,
	0x00, 0x6A, 0x24, 0x00, 0x20, 0x01, 0x0B, 0x02, 0x00, 0x0B, 0x0C, 0x00,
	0x20, 0x00, 0xAD, 0x42, 0x20, 0x86, 0x20, 0x01, 0xAD, 0x84, 0x0B,
}

// handleVirtualOpTrapsModule is handleVirtualOpEchoModule with
// handle_virtual_op's body replaced by an unconditional unreachable trap
// (same code section as instance_activity_test.go's handleActivityTrapsModule).
var handleVirtualOpTrapsModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x11, 0x03, 0x60,
	0x01, 0x7F, 0x01, 0x7F, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x60, 0x02, 0x7F,
	0x7F, 0x01, 0x7E, 0x03, 0x04, 0x03, 0x00, 0x01, 0x02, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B, 0x07,
	0x2D, 0x03, 0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00,
	0x00, 0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65,
	0x00, 0x01, 0x11, 0x68, 0x61, 0x6E, 0x64, 0x6C, 0x65, 0x5F, 0x76, 0x69,
	0x72, 0x74, 0x75, 0x61, 0x6C, 0x5F, 0x6F, 0x70, 0x00, 0x02, 0x0A, 0x1A,
	0x03, 0x11, 0x01, 0x01, 0x7F, 0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20,
	0x00, 0x6A, 0x24, 0x00, 0x20, 0x01, 0x0B, 0x02, 0x00, 0x0B, 0x03, 0x00,
	0x00, 0x0B,
}

// compileVirtualOpFixture compiles testdata/virtualopfixture — a real
// module built on the actual SDK (orm.RegisterVirtualBackend/
// orm.DispatchVirtualOp), not hand-assembled bytecode — to wasip1 WASM,
// the same way adminapi's activitydispatch_test.go compiles
// testdata/activityfixture.
func compileVirtualOpFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "virtualopfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/virtualopfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/virtualopfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// wireVirtualOpRequest/wireVirtualOpResponse mirror sdk/go/orm's own
// unexported virtualOpRequest/virtualOpResponse wire shapes by field name
// and msgpack tag (internal/engine/wasm can't import the module-side
// package's unexported types — the wire contract is the msgpack tags
// themselves, the same boundary any two independently-compiled binaries
// on either side of the WASM ABI cross).
type wireVirtualOpRequest struct {
	Model    string `msgpack:"model"`
	Op       string `msgpack:"op"`
	ID       string `msgpack:"id,omitempty"`
	TenantID string `msgpack:"tenant_id,omitempty"`
}

type wireVirtualOpResponse struct {
	Record map[string]any `msgpack:"record,omitempty"`
}

// TestInvokeHandleVirtualOp_RoundTripsThroughRealModule compiles a real Go
// module registering orm.RegisterVirtualBackend for "legacy.item"
// (testdata/virtualopfixture) and proves InvokeHandleVirtualOp reaches its
// registered Read backend function and returns its response — goerp#373's
// own AC ("round-trips a virtualOpRequest-shaped payload through a real
// compiled module registering RegisterVirtualBackend").
func TestInvokeHandleVirtualOp_RoundTripsThroughRealModule(t *testing.T) {
	wasmBytes := compileVirtualOpFixture(t)

	// A real wasip1 c-shared binary needs far more than the 1 MiB
	// newInstanceForTest's shared runtime caps hand-crafted fixtures at —
	// 64 MiB matches adminapi's own real-module fixture test.
	ctx := context.Background()
	rt := newTestRuntime(t, 64<<20)
	compiled, err := rt.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, "virtualopfixture", compiled, rt.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	t.Cleanup(func() { _ = inst.module.CloseWithExitCode(context.Background(), 0) })

	reqBytes, err := msgpack.Marshal(wireVirtualOpRequest{
		Model:    "legacy.item",
		Op:       "read",
		ID:       "item-1",
		TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	respBytes, err := inst.InvokeHandleVirtualOp(context.Background(), reqBytes)
	if err != nil {
		t.Fatalf("InvokeHandleVirtualOp: %v", err)
	}

	var resp wireVirtualOpResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Record["id"] != "item-1" {
		t.Errorf("record[id] = %v, want %q", resp.Record["id"], "item-1")
	}
	if resp.Record["tenant_id"] != "tenant-1" {
		t.Errorf("record[tenant_id] = %v, want %q", resp.Record["tenant_id"], "tenant-1")
	}
}

func TestInvokeHandleVirtualOp_RoundTripsPayload(t *testing.T) {
	inst := newInstanceForTest(t, handleVirtualOpEchoModule)

	payload := []byte("hello virtual op")
	data, err := inst.InvokeHandleVirtualOp(context.Background(), payload)
	if err != nil {
		t.Fatalf("InvokeHandleVirtualOp: %v", err)
	}
	if string(data) != string(payload) {
		t.Errorf("data = %q, want %q", data, payload)
	}
}

func TestInvokeHandleVirtualOp_TrapSurfacesAsError(t *testing.T) {
	inst := newInstanceForTest(t, handleVirtualOpTrapsModule)

	_, err := inst.InvokeHandleVirtualOp(context.Background(), []byte("payload"))
	if err == nil {
		t.Fatal("expected an error from a handler that traps")
	}
}

func TestInvokeHandleVirtualOp_MissingHandleVirtualOpExportErrors(t *testing.T) {
	inst := newInstanceForTest(t, getDataModule)

	_, err := inst.InvokeHandleVirtualOp(context.Background(), []byte("payload"))
	if err == nil {
		t.Fatal("expected an error when the module has no handle_virtual_op export")
	}
}
