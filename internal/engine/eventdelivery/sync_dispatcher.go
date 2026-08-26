package eventdelivery

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

// SyncDispatcher implements wasm.SyncEventDispatcher, injected into
// wasm.Runtime via Runtime.SetSyncEventDispatcher once ModuleRegistry
// exists (engine.go). Lives in this package, not internal/engine/wasm
// itself, for the same import-cycle reason SubscriberDeliveryWorker does
// (see this package's own doc comment) — resolving another module by
// name needs internal/engine/registry, which internal/engine/wasm can
// never import back.
type SyncDispatcher struct {
	ModuleRegistry *registry.ModuleRegistry
}

// DispatchSync resolves moduleName, borrows an instance from its pool,
// and invokes handle_event — sharing SubscriberDeliveryWorker.Work's
// resolution logic, but synchronous (no River job, no retry: a failure
// here is reported straight back to the emitter as part of the aggregate
// event-system.md §8 describes) and bounded by ctx's own deadline
// (dispatchSyncSubscribers applies it before calling this) rather than a
// River-level job timeout.
func (d *SyncDispatcher) DispatchSync(ctx context.Context, moduleName, handlerName string, payload []byte) (int32, error) {
	snap := d.ModuleRegistry.Snapshot()
	if snap == nil {
		return 0, fmt.Errorf("module registry has no snapshot yet")
	}

	mod, ok := snap.Modules()[moduleName]
	if !ok || mod.Status != module.StatusReady {
		return 0, fmt.Errorf("module %q is not ready", moduleName)
	}
	if mod.Pool == nil {
		return 0, fmt.Errorf("module %q has no WASM instance pool (wasm: false)", moduleName)
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return 0, fmt.Errorf("borrow instance for %s: %w", moduleName, err)
	}
	defer mod.Pool.Return(inst)

	return inst.InvokeHandleEvent(ctx, payload)
}
