package eventdelivery

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

func newTestSyncDispatcher(t *testing.T, wasmBytes []byte) *SyncDispatcher {
	t.Helper()

	w := newTestSubscriberWorker(t, wasmBytes)
	return &SyncDispatcher{ModuleRegistry: w.ModuleRegistry, Invoker: w.Invoker}
}

func testSyncEnvelope(t *testing.T) []byte {
	t.Helper()

	data, err := (event.Envelope{Name: testEventName}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSyncDispatcher_DispatchSync_ZeroPayloadSucceeds(t *testing.T) {
	d := newTestSyncDispatcher(t, buildHandleEventConstStatusModule(0))

	status, err := d.DispatchSync(t.Context(), testEventModuleName, testHandlerName, testSyncEnvelope(t))
	if err != nil {
		t.Fatalf("DispatchSync: %v", err)
	}
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
}

func TestSyncDispatcher_DispatchSync_NonZeroStatusPropagated(t *testing.T) {
	d := newTestSyncDispatcher(t, buildHandleEventConstStatusModule(1))

	status, err := d.DispatchSync(t.Context(), testEventModuleName, testHandlerName, testSyncEnvelope(t))
	if err != nil {
		t.Fatalf("DispatchSync: %v", err)
	}
	if status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
}

func TestSyncDispatcher_DispatchSync_UnknownModuleReturnsError(t *testing.T) {
	d := newTestSyncDispatcher(t, handleEventEchoModule)

	_, err := d.DispatchSync(t.Context(), "does-not-exist", testHandlerName, testSyncEnvelope(t))
	if err == nil {
		t.Fatal("expected an error for an unknown module")
	}
}

func TestSyncDispatcher_DispatchSync_NilSnapshotReturnsError(t *testing.T) {
	d := &SyncDispatcher{ModuleRegistry: &registry.ModuleRegistry{}}

	_, err := d.DispatchSync(t.Context(), testEventModuleName, testHandlerName, testSyncEnvelope(t))
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

	_, err := d.DispatchSync(t.Context(), testEventModuleName, testHandlerName, testSyncEnvelope(t))
	if err == nil {
		t.Fatal("expected an error for a module with a nil Pool")
	}
}
