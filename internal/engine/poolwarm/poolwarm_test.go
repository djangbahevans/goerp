package poolwarm

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// emptyModule is a minimal valid WASM module with no exports — enough for
// InstancePool to instantiate against, same fixture shape as wasm's own
// pool_test.go.
var emptyModule = []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}

func newTestRuntime(t *testing.T) *wasm.Runtime {
	t.Helper()
	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 1 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func newWarmingModule(t *testing.T, rt *wasm.Runtime, warmSize int) *module.LoadedModule {
	t.Helper()
	ctx := context.Background()

	compiled, err := rt.CompileModule(ctx, emptyModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	pool := rt.NewPool("testmod", compiled, wasm.PoolConfig{
		WarmSize: warmSize, MaxSize: warmSize, BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), time.Second) })

	return &module.LoadedModule{Status: module.StatusSyncing, Pool: pool}
}

func TestWarmAll_TransitionsSyncingToWarmingToReady(t *testing.T) {
	rt := newTestRuntime(t)
	m := newWarmingModule(t, rt, 2)
	modules := map[string]*module.LoadedModule{"widgets": m}

	WarmAll(context.Background(), modules)

	if m.Status != module.StatusReady {
		t.Fatalf("Status = %v, want StatusReady", m.Status)
	}
	if got := m.Pool.IdleCount(); got < m.Pool.WarmSize() {
		t.Errorf("IdleCount() = %d, want >= WarmSize (%d)", got, m.Pool.WarmSize())
	}
}

func TestWarmAll_SkipsFailedModules(t *testing.T) {
	failed := &module.LoadedModule{Status: module.StatusFailed}
	modules := map[string]*module.LoadedModule{"broken": failed}

	WarmAll(context.Background(), modules)

	if failed.Status != module.StatusFailed {
		t.Errorf("Status = %v, want unchanged StatusFailed", failed.Status)
	}
}

func TestWarmAll_SkipsModulesWithNilPool(t *testing.T) {
	// A defensive guard, not a state moduleboot.LoadCascading actually
	// produces today (its own cascade-skip path already sets StatusFailed
	// alongside a nil Pool) — but Status != StatusFailed with a nil Pool
	// must still not panic.
	m := &module.LoadedModule{Status: module.StatusSyncing, Pool: nil}
	modules := map[string]*module.LoadedModule{"no-pool": m}

	WarmAll(context.Background(), modules) // must not panic on a nil Pool

	if m.Status != module.StatusSyncing {
		t.Errorf("Status = %v, want unchanged StatusSyncing", m.Status)
	}
}

func TestWarmAll_SlowModuleDoesNotDelayFastModule(t *testing.T) {
	rt := newTestRuntime(t)
	fast := newWarmingModule(t, rt, 1)
	// WarmSize (1000) > MaxSize (1) means idle caps at MaxSize and can
	// never reach WarmSize — a deterministically-never-warms pool, so this
	// test doesn't depend on racing real wall-clock instantiation timing.
	stuck := newBlockedWarmingModule(t, rt)
	modules := map[string]*module.LoadedModule{"fast": fast, "stuck": stuck}

	// A short ctx that only stuck's pool could ever exhaust: if fast's own
	// goroutine were (incorrectly) serialized behind stuck's, it would
	// never reach StatusReady before ctx cancels both — proving
	// independence without polling Status from a second goroutine, which
	// would race against warm()'s writes. WarmAll's own sync.WaitGroup.Wait()
	// establishes happens-before, so reading Status right after WarmAll
	// returns is race-free.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	WarmAll(ctx, modules)

	if fast.Status != module.StatusReady {
		t.Errorf("fast.Status = %v, want StatusReady — it should never wait on stuck", fast.Status)
	}
	if stuck.Status != module.StatusWarming {
		t.Errorf("stuck.Status = %v, want StatusWarming (never reaches WarmSize)", stuck.Status)
	}
}

// newBlockedWarmingModule builds a pool whose WarmSize can never be
// reached: MaxSize caps idle at 1, but WarmSize asks for far more.
func newBlockedWarmingModule(t *testing.T, rt *wasm.Runtime) *module.LoadedModule {
	t.Helper()
	ctx := context.Background()

	compiled, err := rt.CompileModule(ctx, emptyModule)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	pool := rt.NewPool("stuckmod", compiled, wasm.PoolConfig{
		WarmSize: 1000, MaxSize: 1, BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), time.Second) })

	return &module.LoadedModule{Status: module.StatusSyncing, Pool: pool}
}
