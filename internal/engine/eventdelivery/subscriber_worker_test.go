package eventdelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/tetratelabs/wazero"
)

// handleEventEchoModule exports allocate/deallocate/handle_event, where
// handle_event returns the request length as its i32 status — a payload
// of length 0 yields status 0 (success), length 1 yields status 1
// (retryable), length 2 yields status 2 (permanent). Copied from
// internal/engine/wasm's own instance_test.go fixture of the same name
// (unexported there, so not reusable directly) — same pattern
// jobdispatch/worker_test.go already established for handleJobEchoModule.
var handleEventEchoModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x11, 0x03, 0x60,
	0x01, 0x7F, 0x01, 0x7F, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x60, 0x02, 0x7F,
	0x7F, 0x01, 0x7F, 0x03, 0x04, 0x03, 0x00, 0x01, 0x02, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B, 0x07,
	0x28, 0x03, 0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00,
	0x00, 0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65,
	0x00, 0x01, 0x0C, 0x68, 0x61, 0x6E, 0x64, 0x6C, 0x65, 0x5F, 0x65, 0x76,
	0x65, 0x6E, 0x74, 0x00, 0x02, 0x0A, 0x1B, 0x03, 0x11, 0x01, 0x01, 0x7F,
	0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x20,
	0x01, 0x0B, 0x02, 0x00, 0x0B, 0x04, 0x00, 0x20, 0x01, 0x0B,
}

// handleEventTrapsModule is handleEventEchoModule with handle_event's body
// replaced by an unconditional unreachable trap. Copied from
// internal/engine/wasm's own instance_test.go fixture of the same name.
var handleEventTrapsModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x11, 0x03, 0x60,
	0x01, 0x7F, 0x01, 0x7F, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x60, 0x02, 0x7F,
	0x7F, 0x01, 0x7F, 0x03, 0x04, 0x03, 0x00, 0x01, 0x02, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B, 0x07,
	0x28, 0x03, 0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00,
	0x00, 0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65,
	0x00, 0x01, 0x0C, 0x68, 0x61, 0x6E, 0x64, 0x6C, 0x65, 0x5F, 0x65, 0x76,
	0x65, 0x6E, 0x74, 0x00, 0x02, 0x0A, 0x1A, 0x03, 0x11, 0x01, 0x01, 0x7F,
	0x23, 0x00, 0x21, 0x01, 0x20, 0x01, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x20,
	0x01, 0x0B, 0x02, 0x00, 0x0B, 0x03, 0x00, 0x00, 0x0B,
}

// getDataModule exports get_data (not handle_event) — enough to exercise
// the "module missing handle_event export" error path without a real
// module. Copied from internal/engine/wasm's own instance_test.go fixture.
var getDataModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x0A, 0x02, 0x60,
	0x00, 0x01, 0x7E, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x03, 0x03, 0x02, 0x00,
	0x01, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07, 0x19, 0x02, 0x08, 0x67, 0x65,
	0x74, 0x5F, 0x64, 0x61, 0x74, 0x61, 0x00, 0x00, 0x0A, 0x64, 0x65, 0x61,
	0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00, 0x01, 0x0A, 0x0F, 0x02,
	0x0A, 0x00, 0x42, 0x84, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02, 0x0B, 0x02,
	0x00, 0x0B, 0x0B, 0x0B, 0x01, 0x00, 0x41, 0x80, 0x10, 0x0B, 0x04, 0x74,
	0x65, 0x73, 0x74,
}

const (
	testEventModuleName = "testmodule"
	testEventName       = "test.event.happened"
	testHandlerName     = "handle_test_event"
)

// newTestSubscriberWorker builds a real *registry.ModuleRegistry with one
// loaded module (StatusReady, async-subscribed to testEventName, backed by
// a real *wasm.InstancePool compiled from wasmBytes) — the same pattern
// jobdispatch/worker_test.go's newTestWorker establishes.
func newTestSubscriberWorker(t *testing.T, wasmBytes []byte) *SubscriberDeliveryWorker {
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
			Status: module.StatusReady,
			Pool:   pool,
			Manifest: manifest.Manifest{
				Type: "standard",
				Subscribes: []manifest.EventSubscription{
					{Name: testEventName, Handler: testHandlerName, Async: true},
				},
			},
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}

	return &SubscriberDeliveryWorker{ModuleRegistry: reg}
}

func runSubscriberWork(t *testing.T, w *SubscriberDeliveryWorker, args jobqueue.SubscriberDeliveryArgs) error {
	t.Helper()
	return w.Work(context.Background(), &river.Job[jobqueue.SubscriberDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args})
}

func TestSubscriberWork_ZeroPayloadSucceeds(t *testing.T) {
	w := newTestSubscriberWorker(t, handleEventEchoModule)

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err != nil {
		t.Fatalf("Work() error: %v", err)
	}
}

func TestSubscriberWork_RetryableStatusReturnsPlainError(t *testing.T) {
	w := newTestSubscriberWorker(t, handleEventEchoModule)

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, ModuleName: testEventModuleName, HandlerName: testHandlerName,
		Payload: []byte{0}, // length 1 -> status 1 (retryable)
	})
	if err == nil {
		t.Fatal("expected an error for a retryable status")
	}
	if _, ok := errors.AsType[*river.JobCancelError](err); ok {
		t.Fatalf("retryable status must not be a JobCancelError, got %v", err)
	}
}

func TestSubscriberWork_PermanentStatusReturnsJobCancel(t *testing.T) {
	w := newTestSubscriberWorker(t, handleEventEchoModule)

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, ModuleName: testEventModuleName, HandlerName: testHandlerName,
		Payload: []byte{0, 0}, // length 2 -> status 2 (permanent)
	})
	if err == nil {
		t.Fatal("expected an error for a permanent status")
	}
	if _, ok := errors.AsType[*river.JobCancelError](err); !ok {
		t.Fatalf("expected a river.JobCancelError for a permanent status, got %v (%T)", err, err)
	}
}

func TestSubscriberWork_TrapReturnsError(t *testing.T) {
	w := newTestSubscriberWorker(t, handleEventTrapsModule)

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err == nil {
		t.Fatal("expected an error from a handler that traps")
	}
}

func TestSubscriberWork_MissingHandleEventExportReturnsError(t *testing.T) {
	w := newTestSubscriberWorker(t, getDataModule)

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err == nil {
		t.Fatal("expected an error when the module has no handle_event export")
	}
}

func TestSubscriberWork_UnknownModuleReturnsError(t *testing.T) {
	w := newTestSubscriberWorker(t, handleEventEchoModule)

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, ModuleName: "does-not-exist", HandlerName: testHandlerName,
	})
	if err == nil {
		t.Fatal("expected an error for an unknown module")
	}
}

func TestSubscriberWork_StaleSubscriptionReturnsError(t *testing.T) {
	w := newTestSubscriberWorker(t, handleEventEchoModule)

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, ModuleName: testEventModuleName, HandlerName: "not_a_declared_handler",
	})
	if err == nil {
		t.Fatal("expected an error for a subscription no longer registered")
	}
}

// TestSubscriberWork_NilPoolReturnsErrorNotPanic guards against a module
// manifest that legitimately declares wasm: false reaching StatusReady
// with a nil Pool — Work must return an error, not panic on
// mod.Pool.Borrow, the same nil-guard jobdispatch.Worker already proves
// for job dispatch.
func TestSubscriberWork_NilPoolReturnsErrorNotPanic(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		testEventModuleName: {
			Status: module.StatusReady,
			Pool:   nil,
			Manifest: manifest.Manifest{
				Type: "standard",
				Subscribes: []manifest.EventSubscription{
					{Name: testEventName, Handler: testHandlerName, Async: true},
				},
			},
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}
	w := &SubscriberDeliveryWorker{ModuleRegistry: reg}

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err == nil {
		t.Fatal("expected an error for a module with a nil Pool")
	}
}

func TestSubscriberWork_NilSnapshotReturnsError(t *testing.T) {
	w := &SubscriberDeliveryWorker{ModuleRegistry: &registry.ModuleRegistry{}}

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err == nil {
		t.Fatal("expected an error when the registry has no snapshot yet")
	}
}

func TestSubscriberDeliveryWorker_NextRetry_UsesSubscriptionPolicy(t *testing.T) {
	w := newTestSubscriberWorker(t, handleEventEchoModule)
	// Override the fixture's default (no retry_policy) with one declaring
	// a distinctive, easy-to-assert-on delay.
	if _, err := w.ModuleRegistry.Update(map[string]*module.LoadedModule{
		testEventModuleName: {
			Status: module.StatusReady,
			Pool:   w.ModuleRegistry.Snapshot().Modules()[testEventModuleName].Pool,
			Manifest: manifest.Manifest{
				Type: "standard",
				Subscribes: []manifest.EventSubscription{{
					Name: testEventName, Handler: testHandlerName, Async: true,
					RetryPolicy: &manifest.RetryPolicy{
						MaxAttempts: 5, Backoff: "linear", InitialDelayMS: 2000,
					},
				}},
			},
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}

	next := w.NextRetry(&river.Job[jobqueue.SubscriberDeliveryArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1},
		Args:   jobqueue.SubscriberDeliveryArgs{EventName: testEventName, ModuleName: testEventModuleName, HandlerName: testHandlerName},
	})
	got := time.Until(next)
	if got < time.Second || got > 3*time.Second {
		t.Fatalf("NextRetry delay = %v, want ~2s (linear, attempt 1, initial_delay_ms=2000)", got)
	}
}

func TestSubscriberDeliveryWorker_NextRetry_UnknownSubscriptionDefersToClientPolicy(t *testing.T) {
	w := newTestSubscriberWorker(t, handleEventEchoModule)

	next := w.NextRetry(&river.Job[jobqueue.SubscriberDeliveryArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1},
		Args:   jobqueue.SubscriberDeliveryArgs{EventName: testEventName, ModuleName: testEventModuleName, HandlerName: "not_a_declared_handler"},
	})
	if !next.IsZero() {
		t.Fatalf("expected zero time.Time (defer to client policy) for an unknown subscription, got %v", next)
	}
}
