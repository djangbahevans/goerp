package wasm

import (
	"context"
	"testing"
)

// getDataModule exports get_data()->i64, packing (ptr=2048, len=4) pointing
// at a data segment holding "test", plus a no-op deallocate — enough to
// exercise InvokeNoArg's happy path without a real module.
var getDataModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x0A, 0x02, 0x60,
	0x00, 0x01, 0x7E, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x03, 0x03, 0x02, 0x00,
	0x01, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07, 0x19, 0x02, 0x08, 0x67, 0x65,
	0x74, 0x5F, 0x64, 0x61, 0x74, 0x61, 0x00, 0x00, 0x0A, 0x64, 0x65, 0x61,
	0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x01, 0x0A, 0x0F, 0x02,
	0x0A, 0x00, 0x42, 0x84, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02, 0x0B, 0x02,
	0x00, 0x0B, 0x0B, 0x0B, 0x01, 0x00, 0x41, 0x80, 0x10, 0x0B, 0x04, 0x74,
	0x65, 0x73, 0x74,
}

// getDataNoDeallocModule is getDataModule without the deallocate export.
var getDataNoDeallocModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x05, 0x01, 0x60,
	0x00, 0x01, 0x7E, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x0C, 0x01, 0x08, 0x67, 0x65, 0x74, 0x5F, 0x64, 0x61, 0x74, 0x61,
	0x00, 0x00, 0x0A, 0x0C, 0x01, 0x0A, 0x00, 0x42, 0x84, 0x80, 0x80, 0x80,
	0x80, 0x80, 0x02, 0x0B, 0x0B, 0x0B, 0x01, 0x00, 0x41, 0x80, 0x10, 0x0B,
	0x04, 0x74, 0x65, 0x73, 0x74,
}

func newInstanceForTest(t *testing.T, wasmBytes []byte) *ModuleInstance {
	t.Helper()
	ctx := context.Background()
	rt, compiled := compileTestModule(t, wasmBytes)

	inst, err := newModuleInstance(ctx, "testmod", compiled, rt.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	t.Cleanup(func() { _ = inst.module.CloseWithExitCode(context.Background(), 0) })
	return inst
}

func TestNewModuleInstance_WiresExportsAndMemory(t *testing.T) {
	inst := newInstanceForTest(t, getDataModule)

	if inst.module == nil {
		t.Error("module not set")
	}
	if inst.memory == nil {
		t.Error("memory not set")
	}
	if inst.deallocate == nil {
		t.Error("deallocate not wired")
	}
}

func TestNewModuleInstance_RunsInitHookAndFailsOnTrap(t *testing.T) {
	rt, compiled := compileTestModule(t, initTrapsModule)

	_, err := newModuleInstance(context.Background(), "testmod", compiled, rt.wazero)
	if err == nil {
		t.Fatal("expected an error from init()'s trap")
	}
}

func TestInvokeNoArg_ReadsAndDeallocatesResponse(t *testing.T) {
	inst := newInstanceForTest(t, getDataModule)

	data, err := inst.InvokeNoArg(context.Background(), "get_data")
	if err != nil {
		t.Fatalf("InvokeNoArg: %v", err)
	}
	if string(data) != "test" {
		t.Errorf("data = %q, want %q", data, "test")
	}
}

func TestInvokeNoArg_MissingExportErrors(t *testing.T) {
	inst := newInstanceForTest(t, getDataModule)

	_, err := inst.InvokeNoArg(context.Background(), "does_not_exist")
	if err == nil {
		t.Fatal("expected an error for a missing export")
	}
}

func TestInvokeNoArg_MissingDeallocateErrors(t *testing.T) {
	inst := newInstanceForTest(t, getDataNoDeallocModule)

	_, err := inst.InvokeNoArg(context.Background(), "get_data")
	if err == nil {
		t.Fatal("expected an error when the module has no deallocate export")
	}
}
