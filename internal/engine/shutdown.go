package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/rs/zerolog/log"
)

func (e *Engine) Shutdown(ctx context.Context) error {
	log.Info().Msg("shutdown initiated")
	e.readiness.Store(false)

	if e.cfg.ShutdownDrainDelay > 0 {
		time.Sleep(e.cfg.ShutdownDrainDelay)
	}

	if e.cfg.HotReloadEnabled {
		e.shutdownStep(ctx, "hot reload", func(context.Context) error {
			e.hotReload.Stop()
			return nil
		})
	}
	e.shutdownStep(ctx, "tenant config listener", func(context.Context) error {
		e.tenantConfigListener.Stop()
		return nil
	})
	e.shutdownStep(ctx, "roles listener", func(context.Context) error {
		e.rolesListener.Stop()
		return nil
	})
	e.shutdownStep(ctx, "public HTTP server", e.server.Shutdown)
	e.shutdownStep(ctx, "admin HTTP server", e.adminServer.Shutdown)
	e.shutdownStep(ctx, "WebSocket hub", func(ctx context.Context) error {
		e.wsHub.Close(ctx)
		return ctx.Err()
	})

	e.shutdownStep(ctx, "River workers", e.jobQueue.Stop)

	drained := make(map[*wasm.InstancePool]struct{})
	e.drainModulePools(ctx, drained)
	e.shutdownStep(ctx, "registry writes", func(context.Context) error {
		e.moduleRegistry.Lock()
		defer e.moduleRegistry.Unlock()
		return nil
	})
	// A writer can publish a replacement pool while the first snapshot drains.
	e.drainModulePools(ctx, drained)

	e.shutdownStep(ctx, "workflow workers", func(ctx context.Context) error {
		e.workflowWorkers.StopAll(ctx)
		return ctx.Err()
	})
	e.shutdownStep(ctx, "system worker", func(context.Context) error {
		e.systemWorker.Stop()
		return nil
	})
	e.shutdownStep(ctx, "WASM runtime", e.wasmRuntime.Close)

	if e.temporalClient != nil {
		e.shutdownStep(ctx, "Temporal client", func(context.Context) error {
			e.temporalClient.Close()
			return nil
		})
	}
	e.shutdownStep(ctx, "River database pool", func(context.Context) error {
		e.jobQueuePool.Close()
		return nil
	})
	for _, pool := range []struct {
		name string
		db   *sql.DB
	}{
		{name: "primary database pool", db: e.primaryDB},
		{name: "replica database pool", db: e.replicaDB},
		{name: "schema database pool", db: e.syncPool.Raw()},
	} {
		if pool.db != nil {
			e.shutdownStep(ctx, pool.name, func(context.Context) error {
				return pool.db.Close()
			})
		}
	}
	e.shutdownStep(ctx, "Redis clients", func(context.Context) error {
		var idempotencyErr error
		if e.idempotencyCache != nil {
			idempotencyErr = e.idempotencyCache.Close()
		}

		return errors.Join(e.cacheClient.Close(), idempotencyErr)
	})
	if e.tracerProvider != nil {
		e.shutdownStep(ctx, "OpenTelemetry tracer provider", e.tracerProvider.Shutdown)
	}

	log.Info().Msg("shutdown complete")
	return nil
}

func (e *Engine) drainModulePools(ctx context.Context, drained map[*wasm.InstancePool]struct{}) {
	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		return
	}
	modules := snap.Modules()
	for _, name := range slices.Sorted(maps.Keys(modules)) {
		pool := modules[name].Pool
		if pool == nil {
			continue
		}
		if _, ok := drained[pool]; ok {
			continue
		}
		drained[pool] = struct{}{}
		e.shutdownStep(ctx, fmt.Sprintf("module pool %s", name), func(ctx context.Context) error {
			return pool.DrainAndClose(ctx, e.cfg.ShutdownTimeout)
		})
	}
}

func (e *Engine) shutdownStep(ctx context.Context, name string, stop func(context.Context) error) {
	stepCtx, cancel := context.WithTimeout(ctx, e.cfg.ShutdownTimeout)
	defer cancel()

	// Mutex acquisition and connection-pool closure do not accept a context.
	done := make(chan error, 1)
	go func() { done <- stop(stepCtx) }()

	var err error
	select {
	case err = <-done:
		if err == nil {
			err = stepCtx.Err()
		}
	case <-stepCtx.Done():
		err = stepCtx.Err()
	}
	if err != nil {
		log.Warn().Err(err).Str("step", name).Msg("shutdown step failed; continuing")
	}
}
