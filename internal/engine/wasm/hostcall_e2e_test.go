package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

// newHostcallTestRuntime is newHostDBTestRuntime with a larger memory cap
// — a real module linking in sdk/go/db/sdk/go/events/msgpack needs more
// than the 1 MiB the other host_db_test.go/host_event_test.go fixtures
// (which don't import msgpack directly) fit under.
func newHostcallTestRuntime(t *testing.T, primaryDB *sql.DB, maxConcurrentTx int) *Runtime {
	t.Helper()

	rt, err := New(&config.Config{
		CompilationCache:            sharedTestCompilationCacheDir(),
		Environment:                 string(config.Production),
		PoolMaxMemoryByes:           8 << 20,
		DBMaxConcurrentTransactions: maxConcurrentTx,
		SyncSubscriberTimeout:       3 * time.Second,
	}, primaryDB, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

// compileHostcallFixture compiles testdata/hostcallfixture — a real
// module built on the actual sdk/go/db and sdk/go/events packages
// (db.Begin/events.EmitTx/tx.Commit, events.Emit(...,
// events.WithSync())), not hand-assembled bytecode — to wasip1 WASM, the
// same way compileComputedFixture (instance_compute_test.go) compiles
// testdata/computedfixture. Proves goerp#432's acceptance criteria: a
// real compiled module can call out through the module-side host-call
// FFI mechanism, not just a test fixture standing in for one.
func compileHostcallFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "hostcallfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/hostcallfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/hostcallfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// flowResult mirrors testdata/hostcallfixture's own (non-SDK) result
// envelope by field name and msgpack tag.
type flowResult struct {
	OK      bool   `msgpack:"ok"`
	EventID string `msgpack:"event_id,omitempty"`
	Error   string `msgpack:"error,omitempty"`
}

// callHostcallFixture instantiates the compiled fixture under r, sets mc
// as its module context (the same wiring invokeHandler does for a real
// dispatched request), and calls its named no-argument export, decoding
// the returned (ptr,len) i64 into a flowResult.
func callHostcallFixture(t *testing.T, ctx context.Context, r *Runtime, wasmBytes []byte, mc *ModuleContext, export string) flowResult {
	t.Helper()

	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("hostcallfixture-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	fn := inst.module.ExportedFunction(export)
	if fn == nil {
		t.Fatalf("fixture has no export %q", export)
	}
	results, err := fn.Call(ctx)
	if err != nil {
		t.Fatalf("call %s: %v", export, err)
	}

	packed := results[0]
	ptr := uint32(packed >> 32)
	length := uint32(packed)
	raw, ok := inst.module.Memory().Read(ptr, length)
	if !ok {
		t.Fatalf("read result at ptr=%d len=%d: out of bounds", ptr, length)
	}

	var out flowResult
	if err := msgpack.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal flowResult: %v", err)
	}
	return out
}

// TestHostcallFixture_EmitTxCommitFlow_ReachesEventDeliveryQueue is
// goerp#432's first acceptance criterion: a real compiled module calls
// db.Begin(), does work, events.EmitTx(tx, ...), and tx.Commit(), and the
// resulting event actually reaches the engine's event_delivery job
// queue.
func TestHostcallFixture_EmitTxCommitFlow_ReachesEventDeliveryQueue(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	wasmBytes := compileHostcallFixture(t)

	tenantID := uuid.NewString()
	reg := newEmitterEventRegistry("testmodule", "sales.order.confirmed", "")
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, tenantID, "hostcalltest", "trace-1", abi.CapDBWrite|abi.CapEventEmit, nil, ModuleSnapshot{EventRegistry: reg})

	r := newHostcallTestRuntime(t, primaryDB, 10)

	out := callHostcallFixture(t, ctx, r, wasmBytes, mc, "run_emit_tx_flow")
	if !out.OK {
		t.Fatalf("run_emit_tx_flow failed: %s", out.Error)
	}
	if out.EventID == "" {
		t.Fatal("expected a non-empty event ID")
	}

	if got := countEventDeliveryJobs(t, primaryDB, tenantID); got != 1 {
		t.Fatalf("event_delivery job count = %d, want 1", got)
	}
}

// TestHostcallFixture_EmitSync_SubscriberFailureSurfacedAsError is
// goerp#432's second acceptance criterion: events.Emit(...,
// events.WithSync()) from a real compiled module dispatches its event's
// synchronous subscribers inline and surfaces their aggregated failure
// back to the calling module as a returned error.
func TestHostcallFixture_EmitSync_SubscriberFailureSurfacedAsError(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	wasmBytes := compileHostcallFixture(t)

	tenantID := uuid.NewString()
	reg := newEmitterAndSubscriberRegistry("testmodule", "sales.order.shipped",
		manifest.EventSubscription{Name: "sales.order.shipped", Handler: "handle_a", Async: false},
	)
	mc := newSyncTestModuleContext(reg, tenantID)
	r := newHostcallTestRuntime(t, primaryDB, 10)
	dispatcher := &fakeSyncEventDispatcher{results: map[string]struct {
		status int32
		err    error
	}{
		"subscriber-0.handle_a": {status: 0, err: fmt.Errorf("boom")},
	}}
	r.SetSyncEventDispatcher(dispatcher)

	out := callHostcallFixture(t, ctx, r, wasmBytes, mc, "run_emit_sync_flow")
	if out.OK {
		t.Fatal("expected run_emit_sync_flow to surface the subscriber failure as an error")
	}
	if out.Error == "" {
		t.Fatal("expected a non-empty error message")
	}
	if len(dispatcher.calls) != 1 {
		t.Fatalf("expected exactly 1 sync subscriber dispatched, got %v", dispatcher.calls)
	}
}

// TestHostcallFixture_LockFlow_TryLockAndLockBothSucceed is goerp#508's
// acceptance criterion: a real compiled module calls db.Begin(),
// tx.TryLock (acquiring a free lock), tx.Lock (blocking, also free), and
// tx.Commit() — proving sdk/go/db's Lock/TryLock round-trip through the
// real host.db.lock call rather than just host_db_lock_test.go's own
// direct calls into the host function.
func TestHostcallFixture_LockFlow_TryLockAndLockBothSucceed(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	wasmBytes := compileHostcallFixture(t)

	tenantID := uuid.NewString()
	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, tenantID, "hostcalllocktest", "trace-1", abi.CapDBWrite, nil, ModuleSnapshot{})

	r := newHostcallTestRuntime(t, primaryDB, 10)

	out := callHostcallFixture(t, ctx, r, wasmBytes, mc, "run_lock_flow")
	if !out.OK {
		t.Fatalf("run_lock_flow failed: %s", out.Error)
	}
}
