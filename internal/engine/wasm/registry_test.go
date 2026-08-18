package wasm

import (
	"context"
	"testing"
	"time"
)

// newRegistryTestInstance is a stripped-down helper for tests that only
// need a *ModuleInstance to key the registry with — any WASM module with
// the standard allocate/deallocate exports works, boundaryTestModule
// (invoke_test.go) already provides one.
func newRegistryTestInstance(t *testing.T) *ModuleInstance {
	t.Helper()
	ctx := context.Background()

	rt, compiled := compileTestModule(t, boundaryTestModule)

	pool := NewInstancePool("testmod", compiled, rt.wazero, PoolConfig{
		MaxSize: 1, WarmSize: 1, BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(ctx, 10*time.Millisecond) })

	inst, err := pool.Borrow(ctx)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	return inst
}

func TestRuntime_InstanceForModule_UnregisteredReturnsNil(t *testing.T) {
	inst := newRegistryTestInstance(t)

	r := &Runtime{registry: newInstanceRegistry()}
	if got := r.InstanceForModule(inst.Module()); got != nil {
		t.Errorf("expected nil for a module never registered, got %+v", got)
	}
}

func TestRuntime_RegisterInstance_ResolvesByModule(t *testing.T) {
	inst := newRegistryTestInstance(t)

	r := &Runtime{registry: newInstanceRegistry()}
	r.RegisterInstance(inst)

	if got := r.InstanceForModule(inst.Module()); got != inst {
		t.Errorf("InstanceForModule = %+v, want %+v", got, inst)
	}
}

func TestRuntime_UnregisterInstance_ClearsLookup(t *testing.T) {
	inst := newRegistryTestInstance(t)

	r := &Runtime{registry: newInstanceRegistry()}
	r.RegisterInstance(inst)
	r.UnregisterInstance(inst)

	if got := r.InstanceForModule(inst.Module()); got != nil {
		t.Errorf("expected nil after UnregisterInstance, got %+v", got)
	}
}

func TestRuntime_RegisterInstance_DistinguishesModules(t *testing.T) {
	instA := newRegistryTestInstance(t)
	instB := newRegistryTestInstance(t)

	r := &Runtime{registry: newInstanceRegistry()}
	r.RegisterInstance(instA)
	r.RegisterInstance(instB)

	if got := r.InstanceForModule(instA.Module()); got != instA {
		t.Errorf("InstanceForModule(A) = %+v, want %+v", got, instA)
	}
	if got := r.InstanceForModule(instB.Module()); got != instB {
		t.Errorf("InstanceForModule(B) = %+v, want %+v", got, instB)
	}
}
