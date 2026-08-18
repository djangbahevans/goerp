package abi

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/vmihailenco/msgpack/v5"
)

// allocOnlyModule exports a single allocate(size i32) -> i32 bump allocator
// starting at offset 1024 and declares memory {min: 1} — the minimum a
// caller needs to exercise ReadFromModule/WriteToModule/EncodeHostError,
// which only ever touch a module's memory and its allocate export.
var allocOnlyModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x06, 0x01, 0x60,
	0x01, 0x7F, 0x01, 0x7F, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01, 0x00,
	0x01, 0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B, 0x07, 0x0C,
	0x01, 0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x00,
	0x0A, 0x13, 0x01, 0x11, 0x01, 0x01, 0x7F, 0x23, 0x00, 0x21, 0x01, 0x20,
	0x01, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x20, 0x01, 0x0B,
}

type testEnvelope struct {
	OK    bool               `msgpack:"ok"`
	Data  msgpack.RawMessage `msgpack:"data,omitempty"`
	Error *HostError         `msgpack:"error,omitempty"`
}

func newBoundaryTestModule(t *testing.T) (context.Context, wazero.Runtime, wazero.CompiledModule) {
	t.Helper()
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = rt.Close(ctx) })

	compiled, err := rt.CompileModule(ctx, allocOnlyModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })
	return ctx, rt, compiled
}

func TestReadFromModule_RoundTripsWrittenBytes(t *testing.T) {
	ctx, rt, compiled := newBoundaryTestModule(t)
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("m1"))
	if err != nil {
		t.Fatalf("InstantiateModule: %v", err)
	}

	want := []byte("hello boundary")
	if !mod.Memory().Write(1024, want) {
		t.Fatalf("memory.Write failed")
	}

	got, err := ReadFromModule(mod.Memory(), 1024, uint32(len(want)))
	if err != nil {
		t.Fatalf("ReadFromModule: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("ReadFromModule = %q, want %q", got, want)
	}
}

func TestReadFromModule_OutOfBoundsErrors(t *testing.T) {
	ctx, rt, compiled := newBoundaryTestModule(t)
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("m2"))
	if err != nil {
		t.Fatalf("InstantiateModule: %v", err)
	}

	if _, err := ReadFromModule(mod.Memory(), 0xFFFFFFFF, 16); err == nil {
		t.Fatal("expected an out-of-bounds read to error")
	}
}

func TestWriteToModule_RoundTripsThroughEnvelope(t *testing.T) {
	ctx, rt, compiled := newBoundaryTestModule(t)
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("m3"))
	if err != nil {
		t.Fatalf("InstantiateModule: %v", err)
	}
	allocate := mod.ExportedFunction("allocate")

	type payload struct {
		Foo string `msgpack:"foo"`
	}

	packed := WriteToModule(ctx, mod, allocate, payload{Foo: "bar"})
	ptr := uint32(packed >> 32)
	length := uint32(packed)

	raw, ok := mod.Memory().Read(ptr, length)
	if !ok {
		t.Fatalf("memory.Read out of bounds")
	}

	var env testEnvelope
	if err := msgpack.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("expected OK envelope, got error %+v", env.Error)
	}

	var got payload
	if err := msgpack.Unmarshal(env.Data, &got); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if got.Foo != "bar" {
		t.Errorf("Foo = %q, want %q", got.Foo, "bar")
	}
}

func TestEncodeHostError_RoundTripsThroughEnvelope(t *testing.T) {
	ctx, rt, compiled := newBoundaryTestModule(t)
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("m4"))
	if err != nil {
		t.Fatalf("InstantiateModule: %v", err)
	}
	allocate := mod.ExportedFunction("allocate")

	hostErr := &HostError{Code: "db.transaction_not_found", Message: "nope"}
	packed := EncodeHostError(ctx, mod, allocate, hostErr)
	ptr := uint32(packed >> 32)
	length := uint32(packed)

	raw, ok := mod.Memory().Read(ptr, length)
	if !ok {
		t.Fatalf("memory.Read out of bounds")
	}

	var env testEnvelope
	if err := msgpack.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.OK {
		t.Fatal("expected a failure envelope")
	}
	if env.Error == nil || env.Error.Code != hostErr.Code {
		t.Errorf("Error = %+v, want code %q", env.Error, hostErr.Code)
	}
}
