package wasm

import (
	"context"
	"fmt"
	"os"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type Runtime struct {
	wazero       wazero.Runtime
	moduleConfig wazero.ModuleConfig
}

func New(cfg *config.Config) (*Runtime, error) {
	ctx := context.Background()

	cacheDir := cfg.CompilationCache
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("create compilation cache directory: %w", err)
	}

	cache, err := wazero.NewCompilationCacheWithDir(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("open compilation cache: %w", err)
	}

	runtimeCfg := wazero.NewRuntimeConfig().
		WithCompilationCache(cache).
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(cfg.PoolMaxMemoryByes / (64 * manifest.KB))
	if cfg.Environment == string(config.Development) {
		runtimeCfg = runtimeCfg.WithDebugInfoEnabled(true)
	}

	rt := wazero.NewRuntimeWithConfig(ctx, runtimeCfg)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}

	if err := abi.RegisterAll(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("register host abi: %w", err)
	}

	stdout := log.With().Str("component", "wasm").Str("stream", "stdout").Logger()
	stderr := log.With().Str("component", "wasm").Str("stream", "stderr").Logger()

	moduleConfig := wazero.NewModuleConfig().
		WithStdout(stdout).
		WithStderr(stderr)

	return &Runtime{
		wazero:       rt,
		moduleConfig: moduleConfig,
	}, nil
}

func (r *Runtime) ModuleConfig() wazero.ModuleConfig {
	return r.moduleConfig
}

func (r *Runtime) Close(ctx context.Context) error {
	return r.wazero.Close(ctx)
}
