package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/tetratelabs/wazero"
)

// newTestEngine builds an Engine with a real *wasm.Runtime — invokeHandler
// needs one to register/unregister instances and to source a
// ModuleContext's TransactionLimiter, even though these tests never touch
// host.db. db is nil since no test here calls host.db.begin.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	rt, err := wasm.New(&config.Config{
		CompilationCache: filepath.Join(t.TempDir(), "cache"),
		Environment:      string(config.Production),
	}, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	return &Engine{wasmRuntime: rt}
}

// handleRequestEchoModule exports allocate/deallocate/handle_request, where
// handle_request echoes back the same (ptr, len) it was called with, packed
// per the ptr/len return convention — enough to exercise invokeHandler's
// marshal/call/unmarshal round trip without a real module. allocate/
// deallocate bodies are the same bump-allocator/no-op convention already
// established in wasm's own boundary tests.
var handleRequestEchoModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x11, 0x03,
	0x60, 0x01, 0x7F, 0x01, 0x7F, // (i32)->i32          allocate
	0x60, 0x02, 0x7F, 0x7F, 0x00, // (i32,i32)->[]        deallocate
	0x60, 0x02, 0x7F, 0x7F, 0x01, 0x7E, // (i32,i32)->i64  handle_request
	0x03, 0x04, 0x03, 0x00, 0x01, 0x02,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B,
	0x07, 0x2A, 0x03,
	0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x00, // "allocate" func0
	0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x01, // "deallocate" func1
	0x0E, 0x68, 0x61, 0x6E, 0x64, 0x6C, 0x65, 0x5F, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x00, 0x02, // "handle_request" func2
	0x0A, 0x23, 0x03,
	// body0: allocate: ret := next; next += size; return ret
	0x11, 0x01, 0x01, 0x7F,
	0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x20, 0x01, 0x0B,
	// body1: deallocate: no-op
	0x02, 0x00, 0x0B,
	// body2: handle_request: return (i64(ptr) << 32) | i64(len)
	0x0C, 0x00,
	0x20, 0x00, 0xAC, 0x42, 0x20, 0x86, 0x20, 0x01, 0xAC, 0x84, 0x0B,
}

// handleRequestTrapsModule is handleRequestEchoModule with handle_request's
// body replaced by an unconditional `unreachable` trap.
var handleRequestTrapsModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x11, 0x03,
	0x60, 0x01, 0x7F, 0x01, 0x7F,
	0x60, 0x02, 0x7F, 0x7F, 0x00,
	0x60, 0x02, 0x7F, 0x7F, 0x01, 0x7E,
	0x03, 0x04, 0x03, 0x00, 0x01, 0x02,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B,
	0x07, 0x2A, 0x03,
	0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x00,
	0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x01,
	0x0E, 0x68, 0x61, 0x6E, 0x64, 0x6C, 0x65, 0x5F, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x00, 0x02,
	0x0A, 0x1A, 0x03,
	0x11, 0x01, 0x01, 0x7F,
	0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x20, 0x01, 0x0B,
	0x02, 0x00, 0x0B,
	// body2: handle_request: unreachable trap
	0x03, 0x00, 0x00, 0x0B,
}

// newTestInstance compiles wasmBytes against a standalone wazero runtime and
// borrows a real *wasm.ModuleInstance from a pool built on top of it — the
// only exported path to a ModuleInstance from outside the wasm package.
// t.Cleanup drains the pool (stopping replenishLoop) rather than leaking it;
// the instance itself is deliberately never returned to the pool, so the
// drain's own borrowed==0 wait is expected to time out — DrainAndClose still
// stops the background goroutine unconditionally before that wait even
// starts, which is the only thing this cleanup needs.
func newTestInstance(t *testing.T, wasmBytes []byte) *wasm.ModuleInstance {
	t.Helper()
	ctx := context.Background()

	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = rt.Close(ctx) })

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	pool := wasm.NewInstancePool("testmod", compiled, rt, wasm.PoolConfig{
		MaxSize: 1, WarmSize: 1, BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(ctx, 10*time.Millisecond) })

	inst, err := pool.Borrow(ctx)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	return inst
}

func TestInvokeHandler_RoundTripsAndClearsModuleContext(t *testing.T) {
	inst := newTestInstance(t, handleRequestEchoModule)

	req := EngineRequest{
		ID:         "req-1",
		UserID:     "user-1",
		TenantID:   "tenant-1",
		TenantSlug: "acme",
		TraceID:    "trace-1",
	}

	e := newTestEngine(t)
	if _, err := e.invokeHandler(context.Background(), inst, "handler", req, &module.LoadedModule{}); err != nil {
		t.Fatalf("invokeHandler: %v", err)
	}

	if mc := inst.ModuleContext(); mc != nil {
		t.Errorf("expected moduleCtx to be cleared after invokeHandler returns, got %+v", mc)
	}

	if got := e.wasmRuntime.InstanceForModule(inst.Module()); got != nil {
		t.Errorf("expected instanceForModule to be cleared after invokeHandler returns, got %+v", got)
	}
}

func TestInvokeHandler_TrapSurfacesAsError(t *testing.T) {
	inst := newTestInstance(t, handleRequestTrapsModule)

	e := newTestEngine(t)
	_, err := e.invokeHandler(context.Background(), inst, "handler", EngineRequest{ID: "req-1"}, &module.LoadedModule{})
	if err == nil {
		t.Fatal("expected an error from a handler that traps")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Errorf("expected a trap error, not a context error: %v", err)
	}

	if mc := inst.ModuleContext(); mc != nil {
		t.Errorf("expected moduleCtx to be cleared even after a trap, got %+v", mc)
	}
}

func TestInvokeHandler_ContextCancellationSurfacesAsContextError(t *testing.T) {
	inst := newTestInstance(t, handleRequestTrapsModule)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before the call

	e := newTestEngine(t)
	_, err := e.invokeHandler(ctx, inst, "handler", EngineRequest{ID: "req-1"}, &module.LoadedModule{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected the error to wrap context.Canceled, got %v", err)
	}
}
