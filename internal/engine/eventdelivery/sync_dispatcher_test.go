package eventdelivery

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/tetratelabs/wazero"
)

// newTestSyncDispatcher builds a real *registry.ModuleRegistry with one
// StatusReady module backed by a real *wasm.InstancePool compiled from
// wasmBytes — the same pattern newTestSubscriberWorker (subscriber_worker_
// test.go) already establishes for the async path.
func newTestSyncDispatcher(t *testing.T, wasmBytes []byte) *SyncDispatcher {
	t.Helper()
	ctx := context.Background()

	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	pool := wasm.NewInstancePool(testEventModuleName, compiled, rt, wasm.PoolConfig{
		MaxSize:       2,
		BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), time.Second) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		testEventModuleName: {
			Status:   module.StatusReady,
			Pool:     pool,
			Manifest: manifest.Manifest{Type: "standard"},
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}

	return &SyncDispatcher{ModuleRegistry: reg}
}

func TestSyncDispatcher_DispatchSync_ZeroPayloadSucceeds(t *testing.T) {
	d := newTestSyncDispatcher(t, handleEventEchoModule)

	status, err := d.DispatchSync(context.Background(), testEventModuleName, testHandlerName, nil)
	if err != nil {
		t.Fatalf("DispatchSync: %v", err)
	}
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
}

func TestSyncDispatcher_DispatchSync_NonZeroStatusPropagated(t *testing.T) {
	d := newTestSyncDispatcher(t, handleEventEchoModule)

	status, err := d.DispatchSync(context.Background(), testEventModuleName, testHandlerName, []byte{0})
	if err != nil {
		t.Fatalf("DispatchSync: %v", err)
	}
	if status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
}

func TestSyncDispatcher_DispatchSync_UnknownModuleReturnsError(t *testing.T) {
	d := newTestSyncDispatcher(t, handleEventEchoModule)

	_, err := d.DispatchSync(context.Background(), "does-not-exist", testHandlerName, nil)
	if err == nil {
		t.Fatal("expected an error for an unknown module")
	}
}

func TestSyncDispatcher_DispatchSync_NilSnapshotReturnsError(t *testing.T) {
	d := &SyncDispatcher{ModuleRegistry: &registry.ModuleRegistry{}}

	_, err := d.DispatchSync(context.Background(), testEventModuleName, testHandlerName, nil)
	if err == nil {
		t.Fatal("expected an error when the registry has no snapshot yet")
	}
}

func TestSyncDispatcher_DispatchSync_NilPoolReturnsErrorNotPanic(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		testEventModuleName: {Status: module.StatusReady, Pool: nil, Manifest: manifest.Manifest{Type: "standard"}},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}
	d := &SyncDispatcher{ModuleRegistry: reg}

	_, err := d.DispatchSync(context.Background(), testEventModuleName, testHandlerName, nil)
	if err == nil {
		t.Fatal("expected an error for a module with a nil Pool")
	}
}
