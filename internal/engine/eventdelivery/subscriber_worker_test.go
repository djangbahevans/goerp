package eventdelivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

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

func newTestSubscriberWorker(t *testing.T, wasmBytes []byte) *SubscriberDeliveryWorker {
	t.Helper()

	ctx := t.Context()
	cleanupCtx := context.WithoutCancel(ctx)

	rt, err := wasm.New(&config.Config{CompilationCache: wasmtest.SharedCompilationCacheDir(), PoolMaxMemoryByes: 64 << 20, Environment: string(config.Production)}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(cleanupCtx) })

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(cleanupCtx) })

	pool := rt.NewPool(testEventModuleName, compiled, wasm.PoolConfig{
		MaxSize:       2,
		BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(cleanupCtx, time.Second) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		testEventModuleName: {
			Status: module.StatusReady,
			Pool:   pool,
			Manifest: manifest.Manifest{
				Name: testEventModuleName, Type: "standard",
				Subscribes: []manifest.EventSubscription{
					{Name: testEventName, Handler: testHandlerName, Async: true},
				},
			},
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}

	return &SubscriberDeliveryWorker{ModuleRegistry: reg, Invoker: &HandlerInvoker{Runtime: rt, TenantStore: testTenantResolver{}}}
}

func runSubscriberWork(t *testing.T, w *SubscriberDeliveryWorker, args jobqueue.SubscriberDeliveryArgs) error {
	t.Helper()

	return w.Work(t.Context(), &river.Job[jobqueue.SubscriberDeliveryArgs]{JobRow: &rivertype.JobRow{}, Args: args})
}

func TestSubscriberWork_ZeroPayloadSucceeds(t *testing.T) {
	w := newTestSubscriberWorker(t, buildHandleEventConstStatusModule(0))

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, EventVersion: 1, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err != nil {
		t.Fatalf("Work() error: %v", err)
	}
}

func TestSubscriberWork_VersionWithoutSubscriptionIsNotLive(t *testing.T) {
	w := newTestSubscriberWorker(t, buildHandleEventConstStatusModule(0))

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, EventVersion: 2, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err == nil || !strings.Contains(err.Error(), "no longer a registered async subscriber") {
		t.Fatalf("Work() error = %v, want the subscription reported as not registered for version 2", err)
	}
}

func TestSubscriberWork_RetryableStatusReturnsPlainError(t *testing.T) {
	w := newTestSubscriberWorker(t, buildHandleEventConstStatusModule(1))

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, EventVersion: 1, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err == nil {
		t.Fatal("expected an error for a retryable status")
	}
	if _, ok := errors.AsType[*river.JobCancelError](err); ok {
		t.Fatalf("retryable status must not be a JobCancelError, got %v", err)
	}
}

func TestSubscriberWork_PermanentStatusReturnsJobCancel(t *testing.T) {
	w := newTestSubscriberWorker(t, buildHandleEventConstStatusModule(2))

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, EventVersion: 1, ModuleName: testEventModuleName, HandlerName: testHandlerName,
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
		EventName: testEventName, EventVersion: 1, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err == nil {
		t.Fatal("expected an error from a handler that traps")
	}
}

func TestSubscriberWork_MissingHandleEventExportReturnsError(t *testing.T) {
	w := newTestSubscriberWorker(t, getDataModule)

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, EventVersion: 1, ModuleName: testEventModuleName, HandlerName: testHandlerName,
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
		EventName: testEventName, EventVersion: 1, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err == nil {
		t.Fatal("expected an error for a module with a nil Pool")
	}
}

func TestSubscriberWork_NilSnapshotReturnsError(t *testing.T) {
	w := &SubscriberDeliveryWorker{ModuleRegistry: &registry.ModuleRegistry{}}

	err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, EventVersion: 1, ModuleName: testEventModuleName, HandlerName: testHandlerName,
	})
	if err == nil {
		t.Fatal("expected an error when the registry has no snapshot yet")
	}
}

func TestSubscriberDeliveryWorker_NextRetry_UsesSubscriptionPolicy(t *testing.T) {
	w := newTestSubscriberWorker(t, handleEventEchoModule)
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
		Args:   jobqueue.SubscriberDeliveryArgs{EventName: testEventName, EventVersion: 1, ModuleName: testEventModuleName, HandlerName: testHandlerName},
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

type testTenantResolver struct{}

func (testTenantResolver) GetByID(context.Context, string) (*tenant.Tenant, error) {
	return &tenant.Tenant{Slug: "eventtest"}, nil
}
