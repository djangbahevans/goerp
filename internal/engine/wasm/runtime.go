package wasm

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type Runtime struct {
	wazero       wazero.Runtime
	moduleConfig wazero.ModuleConfig
	modules      map[string]api.Module
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
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}

	if err := abi.RegisterAll(ctx, rt); err != nil {
		_ = rt.Close(ctx)
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

func (r *Runtime) alloc(ctx context.Context, module api.Module, size uint64) (uint32, error) {
	fn := module.ExportedFunction("allocate")
	if fn == nil {
		return 0, fmt.Errorf("module missing allocate export")
	}
	results, err := fn.Call(ctx, size)
	if err != nil {
		return 0, fmt.Errorf("allocate %d bytes: %w", size, err)
	}

	ptr := uint32(results[0])
	if ptr == 0 {
		return 0, abi.ErrAllocationFailed
	}

	return ptr, nil
}

func (r *Runtime) dealloc(ctx context.Context, module api.Module, ptr, size uint64) error {
	fn := module.ExportedFunction("deallocate")
	if fn == nil {
		return fmt.Errorf("module missing deallocate export")
	}
	_, err := fn.Call(ctx, ptr, size)
	if err != nil {
		return fmt.Errorf("deallocate %d bytes at ptr=%d: %w", size, ptr, err)
	}

	return nil
}

func (r *Runtime) Call(ctx context.Context, moduleName, fnName string, payload []byte) (int32, error) {
	module, ok := r.modules[moduleName]
	if !ok {
		return 0, fmt.Errorf("could not find module %s", moduleName)
	}

	ptr, err := r.alloc(ctx, module, uint64(len(payload)))
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := r.dealloc(ctx, module, uint64(ptr), uint64(len(payload))); err != nil {
			log.Warn().Err(err).Msg("could not deallocate request buffer")
		}
	}()

	if !module.Memory().Write(uint32(ptr), payload) {
		return 0, fmt.Errorf("memory.Write out of bounds at ptr=%d len=%d", ptr, len(payload))
	}

	fn := module.ExportedFunction(fnName)
	if fn == nil {
		return 0, fmt.Errorf("could not find exported function %s", fnName)
	}
	results, err := fn.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		return 0, fmt.Errorf("call %s: %w", fnName, err)
	}

	return int32(results[0]), nil
}

func (r *Runtime) CallAndRead(ctx context.Context, moduleName string, fnName string, payload []byte) ([]byte, error) {
	module, ok := r.modules[moduleName]
	if !ok {
		return nil, fmt.Errorf("could not find module %s", moduleName)
	}

	ptr, err := r.alloc(ctx, module, uint64(len(payload)))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := r.dealloc(ctx, module, uint64(ptr), uint64(len(payload))); err != nil {
			log.Warn().Err(err).Msg("could not deallocate request buffer")
		}
	}()

	if !module.Memory().Write(uint32(ptr), payload) {
		return nil, fmt.Errorf("memory.Write out of bounds at ptr=%d len=%d", ptr, len(payload))
	}

	fn := module.ExportedFunction(fnName)
	if fn == nil {
		return nil, fmt.Errorf("could not find exported function %s", fnName)
	}
	results, err := fn.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", fnName, err)
	}

	raw := results[0]
	ptr = uint32(raw >> 32)
	length := uint32(raw)
	data, ok := module.Memory().Read(ptr, length)
	if !ok {
		return nil, fmt.Errorf("could not read the data at ptr=%d len=%d", ptr, length)
	}

	if err := r.dealloc(ctx, module, uint64(ptr), uint64(length)); err != nil {
		log.Warn().Err(err).Msg("could not deallocate response buffer")
	}

	return data, nil
}

// tempSeq gives every InstantiateTemp call a distinct wazero module name —
// InstantiateModule rejects a name already in use by a live instance, and
// nothing otherwise guarantees a caller can't have two temp instances of
// the same named module outstanding at once (e.g. a load racing a hot
// reload of the same module).
var tempSeq atomic.Int64

// CompileModule compiles a module's WASM binary to native code, or loads
// it from the on-disk compilation cache keyed by its own checksum (§3
// "Compilation cache" — wazero's own cache, not engine-side logic).
func (r *Runtime) CompileModule(ctx context.Context, binary []byte) (wazero.CompiledModule, error) {
	return r.wazero.CompileModule(ctx, binary)
}

// NewPool builds a module's instance pool against the shared runtime.
func (r *Runtime) NewPool(name string, compiled wazero.CompiledModule, cfg PoolConfig) *InstancePool {
	return NewInstancePool(name, compiled, r.wazero, cfg)
}

// InstantiateTemp creates a throwaway instance for one-off export calls —
// get_routes/get_model_declarations/get_data_migrations at load time
// (engine-internals.md §2 Stage 3 steps 17a-17d). Construction matches a
// pooled instance exactly (same newModuleInstance), so a temp instance
// exposes the same exports and runs the same init hook a pooled instance
// would.
func (r *Runtime) InstantiateTemp(ctx context.Context, name string, compiled wazero.CompiledModule) (*ModuleInstance, error) {
	tempName := fmt.Sprintf("%s-temp-%d", name, tempSeq.Add(1))
	return newModuleInstance(ctx, tempName, compiled, r.wazero)
}
