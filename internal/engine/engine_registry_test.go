package engine

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/tetratelabs/wazero"
)

// newRegistryTestInstance is a stripped-down variant of newTestInstance
// (engine_invoke_test.go) for tests that only need a *wasm.ModuleInstance to
// key the registry with — it never calls invokeHandler, so any WASM module
// with the standard allocate/deallocate exports works.
func newRegistryTestInstance(t *testing.T) *wasm.ModuleInstance {
	t.Helper()
	ctx := context.Background()

	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = rt.Close(ctx) })

	compiled, err := rt.CompileModule(ctx, handleRequestEchoModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	pool := wasm.NewInstancePool("testmod", compiled, rt, wasm.PoolConfig{
		MaxSize: 1, WarmSize: 1, BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(ctx, 10*time.Millisecond) })

	inst, err := pool.Borrow(ctx)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	return inst
}

func TestEngine_InstanceForModule_UnregisteredReturnsNil(t *testing.T) {
	inst := newRegistryTestInstance(t)

	e := &Engine{}
	if got := e.instanceForModule(inst.Module()); got != nil {
		t.Errorf("expected nil for a module never registered, got %+v", got)
	}
}

func TestEngine_RegisterInstance_ResolvesByModule(t *testing.T) {
	inst := newRegistryTestInstance(t)

	e := &Engine{}
	e.registerInstance(inst)

	if got := e.instanceForModule(inst.Module()); got != inst {
		t.Errorf("instanceForModule = %+v, want %+v", got, inst)
	}
}

func TestEngine_UnregisterInstance_ClearsLookup(t *testing.T) {
	inst := newRegistryTestInstance(t)

	e := &Engine{}
	e.registerInstance(inst)
	e.unregisterInstance(inst)

	if got := e.instanceForModule(inst.Module()); got != nil {
		t.Errorf("expected nil after unregisterInstance, got %+v", got)
	}
}

func TestEngine_RegisterInstance_DistinguishesModules(t *testing.T) {
	instA := newRegistryTestInstance(t)
	instB := newRegistryTestInstance(t)

	e := &Engine{}
	e.registerInstance(instA)
	e.registerInstance(instB)

	if got := e.instanceForModule(instA.Module()); got != instA {
		t.Errorf("instanceForModule(A) = %+v, want %+v", got, instA)
	}
	if got := e.instanceForModule(instB.Module()); got != instB {
		t.Errorf("instanceForModule(B) = %+v, want %+v", got, instB)
	}
}
