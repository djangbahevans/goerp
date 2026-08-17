// Package poolwarm runs Stage 5 of the engine startup sequence
// (engine-internals.md §2): wait for each loaded module's InstancePool —
// already replenishing in the background since Stage 3 — to reach its
// configured WarmSize, advancing LoadedModule.Status from StatusSyncing
// through StatusWarming to StatusReady. Mutating Status here is enough to
// reach any registry.RegistrySnapshot already published from the same
// modules map — a snapshot holds the same *LoadedModule pointers, not a
// copy, so no second registry.Update call is needed.
package poolwarm

import (
	"context"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
)

const pollInterval = 10 * time.Millisecond

// WarmAll waits for every non-StatusFailed module's pool to reach its
// configured WarmSize, one goroutine per module so a slow pool never
// delays another module's transition to StatusReady. Returns once every
// module has reached StatusReady or ctx is done.
func WarmAll(ctx context.Context, modules map[string]*module.LoadedModule) {
	var wg sync.WaitGroup
	for _, m := range modules {
		if m.Status == module.StatusFailed || m.Pool == nil {
			continue
		}
		wg.Add(1)
		go func(m *module.LoadedModule) {
			defer wg.Done()
			warm(ctx, m)
		}(m)
	}
	wg.Wait()
}

func warm(ctx context.Context, m *module.LoadedModule) {
	m.Status = module.StatusWarming

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for m.Pool.IdleCount() < m.Pool.WarmSize() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}

	m.Status = module.StatusReady
}
