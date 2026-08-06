package wasm

import (
	"context"
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero/api"
)

// A real WASM module exercising the host/module boundary convention:
//   - allocate(size i32) -> i32: bump allocator starting at offset 1024.
//   - deallocate(ptr i32, size i32): no-op.
//   - echo(ptr i32, len i32) -> i64: returns the same (ptr, len) it was given,
//     packed as the ptr/len convention (upper 32 bits ptr, lower 32 bits len).
//   - status(ptr i32, len i32) -> i32: returns len verbatim as a status code,
//     so an empty payload yields 0 (success) and a non-empty one yields a
//     non-zero "error code" equal to the payload length.
var boundaryTestModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	// type section: 4 types
	0x01, 0x17, 0x04,
	0x60, 0x01, 0x7F, 0x01, 0x7F, // (i32)->i32          allocate
	0x60, 0x02, 0x7F, 0x7F, 0x00, // (i32,i32)->[]        deallocate
	0x60, 0x02, 0x7F, 0x7F, 0x01, 0x7E, // (i32,i32)->i64  echo
	0x60, 0x02, 0x7F, 0x7F, 0x01, 0x7F, // (i32,i32)->i32  status
	// function section
	0x03, 0x05, 0x04, 0x00, 0x01, 0x02, 0x03,
	// memory section: min=1
	0x05, 0x03, 0x01, 0x00, 0x01,
	// global section: mutable i32 = 1024, the bump allocator's next-free pointer
	0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B,
	// export section
	0x07, 0x29, 0x04,
	0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x00, // "allocate" func0
	0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x01, // "deallocate" func1
	0x04, 0x65, 0x63, 0x68, 0x6F, 0x00, 0x02, // "echo" func2
	0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x00, 0x03, // "status" func3
	// code section
	0x0A, 0x28, 0x04,
	// body1: allocate: ret := next; next += size; return ret
	0x11, 0x01, 0x01, 0x7F,
	0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x20, 0x01, 0x0B,
	// body2: deallocate: no-op
	0x02, 0x00, 0x0B,
	// body3: echo: return (i64(ptr) << 32) | i64(len)
	0x0C, 0x00,
	0x20, 0x00, 0xAC, 0x42, 0x20, 0x86, 0x20, 0x01, 0xAC, 0x84, 0x0B,
	// body4: status: return len
	0x04, 0x00, 0x20, 0x01, 0x0B,
}

// alwaysSucceedsModule is boundaryTestModule with status(ptr, len) -> i32
// changed to unconditionally return 0, since a msgpack-encoded request is
// never actually zero bytes (even nil marshals to a 1-byte "nil" value) --
// boundaryTestModule's len-echoing status can exercise the non-zero/error
// path but never the success path once payloads go through Invoke/InvokeStatus.
var alwaysSucceedsModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x17, 0x04,
	0x60, 0x01, 0x7F, 0x01, 0x7F,
	0x60, 0x02, 0x7F, 0x7F, 0x00,
	0x60, 0x02, 0x7F, 0x7F, 0x01, 0x7E,
	0x60, 0x02, 0x7F, 0x7F, 0x01, 0x7F,
	0x03, 0x05, 0x04, 0x00, 0x01, 0x02, 0x03,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B,
	0x07, 0x29, 0x04,
	0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x00,
	0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x01,
	0x04, 0x65, 0x63, 0x68, 0x6F, 0x00, 0x02,
	0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x00, 0x03,
	0x0A, 0x28, 0x04,
	0x11, 0x01, 0x01, 0x7F,
	0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x20, 0x01, 0x0B,
	0x02, 0x00, 0x0B,
	0x0C, 0x00,
	0x20, 0x00, 0xAC, 0x42, 0x20, 0x86, 0x20, 0x01, 0xAC, 0x84, 0x0B,
	0x04, 0x00, 0x41, 0x00, 0x0B, // status: i32.const 0; end
}

// allocateFailsModule exports only allocate(size i32) -> i32, always
// returning 0 to simulate the module itself failing to satisfy an
// allocation.
var allocateFailsModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x06, 0x01, 0x60, 0x01, 0x7F, 0x01, 0x7F, // type: (i32)->i32
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x0C, 0x01, 0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x00, // export "allocate"
	0x0A, 0x06, 0x01, 0x04, 0x00, 0x41, 0x00, 0x0B, // code: i32.const 0; end
}

// registerTestModule compiles and instantiates wasmBytes against rt's real
// wazero runtime and registers it under name, exactly as a module-loading
// pipeline would, so Call/CallAndRead/Invoke exercise the real host/module
// boundary rather than a mock.
func registerTestModule(t *testing.T, rt *Runtime, name string, wasmBytes []byte) api.Module {
	t.Helper()
	ctx := context.Background()

	mod, err := rt.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(ctx) })

	inst, err := rt.wazero.InstantiateModule(ctx, mod, rt.ModuleConfig().WithName(name))
	if err != nil {
		t.Fatalf("InstantiateModule: %v", err)
	}
	t.Cleanup(func() { _ = inst.Close(ctx) })

	if rt.modules == nil {
		rt.modules = make(map[string]api.Module)
	}
	rt.modules[name] = inst

	return inst
}

func TestCallAndRead_RoundTripsPayloadThroughEcho(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, 64*1024)
	registerTestModule(t, rt, "boundary", boundaryTestModule)

	payload := []byte("hello wasm boundary")
	got, err := rt.CallAndRead(ctx, "boundary", "echo", payload)
	if err != nil {
		t.Fatalf("CallAndRead: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestCall_ReturnsPlainStatusCode(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, 64*1024)
	registerTestModule(t, rt, "boundary", boundaryTestModule)

	status, err := rt.Call(ctx, "boundary", "status", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if status != 0 {
		t.Fatalf("status = %d, want 0 for an empty payload", status)
	}

	status, err = rt.Call(ctx, "boundary", "status", []byte("abcde"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if status != 5 {
		t.Fatalf("status = %d, want 5 (the payload length echoed back as the error code)", status)
	}
}

func TestCall_UnknownModule(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, 64*1024)

	if _, err := rt.Call(ctx, "does-not-exist", "status", nil); err == nil {
		t.Fatal("expected an error for an unregistered module, got nil")
	}
}

type invokePayload struct {
	Name  string `msgpack:"name"`
	Count int    `msgpack:"count"`
}

func TestInvoke_MarshalsAndUnmarshalsThroughEcho(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, 64*1024)
	registerTestModule(t, rt, "boundary", boundaryTestModule)

	req := invokePayload{Name: "widget", Count: 7}
	resp, err := Invoke[invokePayload, invokePayload](ctx, rt, "boundary", "echo", req)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp != req {
		t.Fatalf("resp = %+v, want %+v (echo should round-trip the msgpack-encoded request)", resp, req)
	}
}

func TestInvokeStatus_Success(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, 64*1024)
	registerTestModule(t, rt, "always-succeeds", alwaysSucceedsModule)

	if err := InvokeStatus(ctx, rt, "always-succeeds", "status", invokePayload{Name: "x"}); err != nil {
		t.Fatalf("InvokeStatus against a module whose status always returns 0: expected success, got %v", err)
	}
}

func TestInvokeStatus_NonZeroCodeSurfacesAsError(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, 64*1024)
	registerTestModule(t, rt, "boundary", boundaryTestModule)

	if err := InvokeStatus(ctx, rt, "boundary", "status", invokePayload{Name: "x"}); err == nil {
		t.Fatal("InvokeStatus against a module whose status echoes the payload length: expected a non-zero status to surface as an error, got nil")
	}
}

func TestAlloc_ModuleReturningZeroSurfacesAllocationFailed(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, 64*1024)
	registerTestModule(t, rt, "failing", allocateFailsModule)

	_, err := rt.CallAndRead(ctx, "failing", "anything", []byte("x"))
	if !errors.Is(err, abi.ErrAllocationFailed) {
		t.Fatalf("err = %v, want errors.Is(err, abi.ErrAllocationFailed)", err)
	}
}
