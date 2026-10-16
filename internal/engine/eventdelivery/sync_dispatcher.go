package eventdelivery

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/vmihailenco/msgpack/v5"
)

type SyncDispatcher struct {
	ModuleRegistry *registry.ModuleRegistry
	Invoker        *HandlerInvoker
	Metrics        *Metrics
}

func (d *SyncDispatcher) DispatchSync(ctx context.Context, moduleName, handlerName string, payload []byte) (status int32, err error) {
	var env event.Envelope
	if err := msgpack.Unmarshal(payload, &env); err != nil {
		return 0, fmt.Errorf("decode event envelope: %w", err)
	}
	defer func() {
		if err != nil || status != 0 {
			d.Metrics.observeFailure(env.Name, moduleName)
		}
	}()

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

	return d.Invoker.invoke(ctx, snap, mod, env)
}
