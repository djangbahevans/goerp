package wasm

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
)

// A minimal WASM module declaring memory {min: 1, max: 100} pages, exported as "mem".
var wasmModuleWithMemory = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, // magic, version
	0x05, 0x04, 0x01, 0x01, 0x01, 0x64, // memory section: limits{min:1, max:100}
	0x07, 0x07, 0x01, 0x03, 0x6D, 0x65, 0x6D, 0x02, 0x00, // export "mem" -> memory 0
}

func newTestRuntime(t *testing.T, maxMemoryBytes uint32) *Runtime {
	t.Helper()

	cfg := &config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: maxMemoryBytes,
		Environment:       string(config.Production),
	}

	rt, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return rt
}

func TestNew_MemoryLimitClampsOversizedModuleAtCompileTime(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, 64*1024) // one page

	mod, err := rt.wazero.CompileModule(ctx, wasmModuleWithMemory)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(ctx) })

	max, ok := mod.ExportedMemories()["mem"].Max()
	if !ok {
		t.Fatal("expected exported memory to report a max")
	}
	if max != 1 {
		t.Fatalf("expected wazero to clamp the module's declared max (100 pages) down to the engine's configured limit (1 page), got %d", max)
	}
}

func TestNew_MemoryGrowBeyondLimitFailsGracefully(t *testing.T) {
	ctx := context.Background()
	rt := newTestRuntime(t, 64*1024) // one page

	mod, err := rt.wazero.CompileModule(ctx, wasmModuleWithMemory)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = mod.Close(ctx) })

	inst, err := rt.wazero.InstantiateModule(ctx, mod, rt.ModuleConfig())
	if err != nil {
		t.Fatalf("InstantiateModule: %v", err)
	}
	t.Cleanup(func() { _ = inst.Close(ctx) })

	mem := inst.Memory()
	if size := mem.Size(); size != 65536 {
		t.Fatalf("expected initial memory size of 65536 bytes (1 page), got %d", size)
	}

	result, ok := mem.Grow(200)
	if ok {
		t.Fatalf("expected Grow to fail once the engine memory limit is exceeded, got result=%d ok=%v", result, ok)
	}
	if result != 0 {
		t.Fatalf("expected Grow to return 0 on failure, got %d", result)
	}
}

func TestRuntime_CompileModule_Succeeds(t *testing.T) {
	rt := newTestRuntime(t, 1<<20)

	compiled, err := rt.CompileModule(context.Background(), emptyModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
}

func TestRuntime_NewPool_BuildsAUsablePool(t *testing.T) {
	rt := newTestRuntime(t, 1<<20)
	compiled, err := rt.CompileModule(context.Background(), emptyModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	pool := rt.NewPool("testmod", compiled, PoolConfig{MaxSize: 1, WarmSize: 1, BorrowTimeout: time.Second})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 10*time.Millisecond) })

	inst, err := pool.Borrow(context.Background())
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	if inst == nil {
		t.Fatal("expected a non-nil instance")
	}
}

func TestRuntime_InstantiateTemp_ReturnsAUsableInstance(t *testing.T) {
	rt := newTestRuntime(t, 1<<20)
	compiled, err := rt.CompileModule(context.Background(), emptyModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := rt.InstantiateTemp(context.Background(), "testmod", compiled)
	if err != nil {
		t.Fatalf("InstantiateTemp: %v", err)
	}
	t.Cleanup(func() { _ = inst.Module().Close(context.Background()) })
}

func TestRuntime_InstantiateTemp_NamesDontCollideAcrossCalls(t *testing.T) {
	rt := newTestRuntime(t, 1<<20)
	compiled, err := rt.CompileModule(context.Background(), emptyModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	instA, err := rt.InstantiateTemp(context.Background(), "testmod", compiled)
	if err != nil {
		t.Fatalf("InstantiateTemp (A): %v", err)
	}
	t.Cleanup(func() { _ = instA.Module().Close(context.Background()) })

	// Without a distinct wazero module name per call, this second
	// instantiation (of the same module name, while the first is still
	// open) would fail — wazero rejects a name already in use.
	instB, err := rt.InstantiateTemp(context.Background(), "testmod", compiled)
	if err != nil {
		t.Fatalf("InstantiateTemp (B): %v", err)
	}
	t.Cleanup(func() { _ = instB.Module().Close(context.Background()) })
}
