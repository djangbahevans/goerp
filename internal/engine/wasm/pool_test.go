package wasm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// emptyModule is a minimal valid WASM module with no exports at all. It's
// enough to exercise pool mechanics — instantiate() doesn't error just
// because allocate/deallocate/handleRequest/handleEvent/handleJob/init are
// missing, since ExportedFunction returns nil rather than an error.
var emptyModule = []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}

// initTrapsModule exports a single "init" function, type ()->(), whose body
// is an unconditional `unreachable` trap — so instantiate() must fail at the
// init() call, exercising the failed-instantiation path.
var initTrapsModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type section: () -> ()
	0x03, 0x02, 0x01, 0x00, // function section: func0 : type0
	0x07, 0x08, 0x01, 0x04, 0x69, 0x6E, 0x69, 0x74, 0x00, 0x00, // export "init" -> func0
	0x0A, 0x05, 0x01, 0x03, 0x00, 0x00, 0x0B, // code: locals=0, unreachable, end
}

func newTestPool(t *testing.T, wasmBytes []byte, cfg PoolConfig) *InstancePool {
	t.Helper()
	ctx := context.Background()
	rt := newTestRuntime(t, 1<<20)

	compiled, err := rt.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	return NewInstancePool("testmod", compiled, rt.wazero, cfg)
}

func TestInstancePool_Borrow_DirectInstantiateWhenTokenFree(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{MaxSize: 2, BorrowTimeout: time.Second})

	inst, err := pool.Borrow(context.Background())
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	if inst == nil {
		t.Fatal("expected a non-nil instance")
	}
	if !inst.inUse.Load() {
		t.Error("expected inUse to be true after Borrow")
	}
	if got := pool.borrowed.Load(); got != 1 {
		t.Errorf("borrowed = %d, want 1", got)
	}
	if got := pool.created.Load(); got != 1 {
		t.Errorf("created = %d, want 1", got)
	}
}

func TestInstancePool_Borrow_WaitsThenSucceedsOnceTokenFrees(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{MaxSize: 1, BorrowTimeout: 2 * time.Second})

	inst1, err := pool.Borrow(context.Background())
	if err != nil {
		t.Fatalf("first Borrow: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		pool.Return(inst1)
	}()

	start := time.Now()
	inst2, err := pool.Borrow(context.Background())
	if err != nil {
		t.Fatalf("second Borrow: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("second Borrow returned after %v, expected to wait for the first Return", elapsed)
	}
	if inst2 == nil {
		t.Fatal("expected a non-nil instance once the token freed")
	}
}

func TestInstancePool_Borrow_TimesOutWhenExhausted(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{MaxSize: 1, BorrowTimeout: 50 * time.Millisecond})

	if _, err := pool.Borrow(context.Background()); err != nil {
		t.Fatalf("first Borrow: %v", err)
	}

	start := time.Now()
	_, err := pool.Borrow(context.Background())
	if !errors.Is(err, ErrPoolTimeout) {
		t.Fatalf("second Borrow error = %v, want ErrPoolTimeout", err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("second Borrow returned after %v, want at least BorrowTimeout (50ms)", elapsed)
	}
}

func TestInstancePool_Borrow_ContextCancellationTimesOutBeforeBorrowTimeout(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{MaxSize: 1, BorrowTimeout: 5 * time.Second})

	if _, err := pool.Borrow(context.Background()); err != nil {
		t.Fatalf("first Borrow: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := pool.Borrow(ctx)
	if !errors.Is(err, ErrPoolTimeout) {
		t.Fatalf("second Borrow error = %v, want ErrPoolTimeout", err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("second Borrow took %v, expected context cancellation to win before BorrowTimeout", elapsed)
	}
}

func TestInstancePool_Borrow_ReturnsErrPoolDrainingImmediately(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{MaxSize: 1, BorrowTimeout: 5 * time.Second})

	pool.mu.Lock()
	pool.draining = true
	pool.mu.Unlock()

	start := time.Now()
	_, err := pool.Borrow(context.Background())
	if !errors.Is(err, ErrPoolDraining) {
		t.Fatalf("Borrow error = %v, want ErrPoolDraining", err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("Borrow took %v, expected an immediate ErrPoolDraining without waiting", elapsed)
	}
}

func TestInstancePool_Return_ReleasesTokenAndNeverWritesIdle(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{MaxSize: 1, BorrowTimeout: time.Second})

	inst, err := pool.Borrow(context.Background())
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	pool.Return(inst)

	if got := pool.borrowed.Load(); got != 0 {
		t.Errorf("borrowed = %d, want 0 after Return", got)
	}
	if got := pool.closed.Load(); got != 1 {
		t.Errorf("closed = %d, want 1 after Return", got)
	}
	if n := len(pool.idle); n != 0 {
		t.Errorf("len(idle) = %d, want 0 — Return must never write to idle", n)
	}

	// The released token should let another Borrow proceed immediately.
	start := time.Now()
	if _, err := pool.Borrow(context.Background()); err != nil {
		t.Fatalf("Borrow after Return: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
		t.Errorf("Borrow after Return took %v, expected the freed token to be used immediately", elapsed)
	}
}

func TestInstancePool_NeverExceedsMaxSize(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{MaxSize: 2, BorrowTimeout: 30 * time.Millisecond})

	if _, err := pool.Borrow(context.Background()); err != nil {
		t.Fatalf("Borrow 1: %v", err)
	}
	if _, err := pool.Borrow(context.Background()); err != nil {
		t.Fatalf("Borrow 2: %v", err)
	}

	if _, err := pool.Borrow(context.Background()); !errors.Is(err, ErrPoolTimeout) {
		t.Fatalf("Borrow 3 error = %v, want ErrPoolTimeout — pool must never exceed MaxSize", err)
	}
}

func TestInstancePool_Metrics_BorrowedReturnsToZero(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{MaxSize: 3, BorrowTimeout: time.Second})

	var insts []*ModuleInstance
	for i := range 3 {
		inst, err := pool.Borrow(context.Background())
		if err != nil {
			t.Fatalf("Borrow %d: %v", i, err)
		}
		insts = append(insts, inst)
	}
	if got := pool.borrowed.Load(); got != 3 {
		t.Fatalf("borrowed = %d, want 3", got)
	}

	for _, inst := range insts {
		pool.Return(inst)
	}
	if got := pool.borrowed.Load(); got != 0 {
		t.Errorf("borrowed = %d, want 0 once every borrow is returned", got)
	}
	if got := pool.created.Load(); got != 3 {
		t.Errorf("created = %d, want 3", got)
	}
	if got := pool.closed.Load(); got != 3 {
		t.Errorf("closed = %d, want 3", got)
	}
}

func TestInstancePool_Metrics_WaitTimeHistogramObserves(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{MaxSize: 1, BorrowTimeout: time.Second})

	if _, err := pool.Borrow(context.Background()); err != nil {
		t.Fatalf("Borrow: %v", err)
	}

	var m dto.Metric
	if err := pool.waitTime.(prometheus.Metric).Write(&m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := m.GetHistogram().GetSampleCount(); got != 1 {
		t.Errorf("waitTime sample count = %d, want 1", got)
	}
}

func TestInstancePool_FailedInstantiateDoesNotIncrementCreated(t *testing.T) {
	pool := newTestPool(t, initTrapsModule, PoolConfig{MaxSize: 1, BorrowTimeout: time.Second})

	if _, err := pool.Borrow(context.Background()); err == nil {
		t.Fatal("expected Borrow to fail against a module whose init() traps")
	}
	if got := pool.created.Load(); got != 0 {
		t.Errorf("created = %d, want 0 — a failed instantiate/init() must not count as created", got)
	}
	if n := len(pool.tokens); n != 0 {
		t.Errorf("len(tokens) = %d, want 0 — the token must be released on a failed instantiate", n)
	}

	// The released token should let a second (also failing) Borrow proceed
	// immediately rather than blocking on an exhausted pool.
	start := time.Now()
	if _, err := pool.Borrow(context.Background()); err == nil {
		t.Fatal("expected second Borrow to also fail")
	}
	if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
		t.Errorf("second Borrow took %v, expected the token released by the first failure to be reused immediately", elapsed)
	}
}

func TestPoolConfig_WithDefaults(t *testing.T) {
	cfg := PoolConfig{}.withDefaults()
	if cfg.WarmSize != 4 {
		t.Errorf("WarmSize = %d, want 4", cfg.WarmSize)
	}
	if cfg.MaxSize != 16 {
		t.Errorf("MaxSize = %d, want 16", cfg.MaxSize)
	}
	if cfg.BorrowTimeout != 5*time.Second {
		t.Errorf("BorrowTimeout = %v, want 5s", cfg.BorrowTimeout)
	}
	if cfg.MaxMemoryPages != 256 {
		t.Errorf("MaxMemoryPages = %d, want 256", cfg.MaxMemoryPages)
	}

	// Explicitly-set fields must survive, not just zero-valued ones.
	custom := PoolConfig{MaxSize: 8}.withDefaults()
	if custom.MaxSize != 8 {
		t.Errorf("MaxSize = %d, want 8 (explicit value must not be overwritten)", custom.MaxSize)
	}
	if custom.WarmSize != 4 {
		t.Errorf("WarmSize = %d, want 4 (default fills only the zero field)", custom.WarmSize)
	}
}

func TestNewInstancePool_AppliesDefaultsToChannelCapacities(t *testing.T) {
	pool := newTestPool(t, emptyModule, PoolConfig{})

	if cap(pool.tokens) != 16 {
		t.Errorf("cap(tokens) = %d, want 16 (default MaxSize)", cap(pool.tokens))
	}
	if cap(pool.idle) != 4 {
		t.Errorf("cap(idle) = %d, want 4 (default WarmSize)", cap(pool.idle))
	}
}
