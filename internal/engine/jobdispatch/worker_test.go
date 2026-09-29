package jobdispatch

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// getDataModule exports get_data (not handle_job) — enough to exercise
// the "module missing handle_job export" error path without a real
// module. Copied from internal/engine/wasm's own instance_test.go fixture
// of the same name (unexported there, so not reusable directly).
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

// handleJobTrapsModule exports allocate/deallocate/handle_job, where
// handle_job's body is an unconditional unreachable trap. Copied from
// internal/engine/wasm's own instance_test.go fixture of the same name.
var handleJobTrapsModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x11, 0x03, 0x60,
	0x01, 0x7F, 0x01, 0x7F, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x60, 0x02, 0x7F,
	0x7F, 0x01, 0x7F, 0x03, 0x04, 0x03, 0x00, 0x01, 0x02, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x06, 0x07, 0x01, 0x7F, 0x01, 0x41, 0x80, 0x08, 0x0B, 0x07,
	0x26, 0x03, 0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65, 0x00,
	0x00, 0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61, 0x74, 0x65,
	0x00, 0x01, 0x0A, 0x68, 0x61, 0x6E, 0x64, 0x6C, 0x65, 0x5F, 0x6A, 0x6F,
	0x62, 0x00, 0x02, 0x0A, 0x1A, 0x03, 0x11, 0x01, 0x01, 0x7F, 0x23, 0x00,
	0x21, 0x01, 0x20, 0x01, 0x20, 0x00, 0x6A, 0x24, 0x00, 0x20, 0x01, 0x0B,
	0x02, 0x00, 0x0B, 0x03, 0x00, 0x00, 0x0B,
}

const testModuleName = "testmodule"
const testJobType = "test_job"

// newTestWorker builds a real *registry.ModuleRegistry with one loaded
// module (StatusReady, declaring testJobType, backed by a real
// *wasm.InstancePool compiled from wasmBytes) — going through the real
// ModuleRegistry.Update pipeline, the same pattern
// eventdelivery/worker_test.go's newTestModuleRegistry establishes,
// extended with a real WASM pool since (unlike eventdelivery.Worker) this
// worker actually invokes WASM. Also wires a real Runtime/TenantStore and
// returns a real fixture tenant's ID (goerp#500): Work now resolves the
// tenant slug through TenantStore.GetByID before invoking handle_job, so
// every test job needs a real tenant row, not just a module registry.
func newTestWorker(t *testing.T, wasmBytes []byte) (*Worker, string) {
	t.Helper()
	ctx := context.Background()

	conn, tenantStore := newTestTenantStore(t)
	tt := newFixtureTenant(t, conn, tenantStore)
	rt := newTestWasmRuntime(t)

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	pool := rt.NewPool(testModuleName, compiled, wasm.PoolConfig{
		MaxSize:       2,
		BorrowTimeout: time.Second,
	})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), time.Second) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		testModuleName: {
			Status: module.StatusReady,
			Pool:   pool,
			Manifest: manifest.Manifest{
				Type:     "standard",
				JobTypes: []manifest.JobType{{Name: testJobType, Handler: "handle_test_job"}},
			},
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}

	return &Worker{ModuleRegistry: reg, Runtime: rt, TenantStore: tenantStore}, tt.ID
}

func runWork(t *testing.T, w *Worker, args jobqueue.WASMJobArgs) error {
	t.Helper()
	return w.Work(context.Background(), &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{}, Args: args})
}

func TestWork_SuccessStatusSucceeds(t *testing.T) {
	w, tenantID := newTestWorker(t, buildHandleJobConstStatusModule(0))

	err := runWork(t, w, jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType, TenantID: tenantID, Payload: nil})
	if err != nil {
		t.Fatalf("Work() error: %v", err)
	}
}

func TestWork_RetryableStatusReturnsPlainError(t *testing.T) {
	for _, status := range []int32{1, 7} {
		w, tenantID := newTestWorker(t, buildHandleJobConstStatusModule(status))

		err := runWork(t, w, jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType, TenantID: tenantID})
		if err == nil {
			t.Fatalf("status %d: expected an error", status)
		}
		if _, ok := errors.AsType[*river.JobCancelError](err); ok {
			t.Fatalf("status %d must not be a JobCancelError, got %v", status, err)
		}
	}
}

func TestWork_PermanentStatusReturnsJobCancel(t *testing.T) {
	w, tenantID := newTestWorker(t, buildHandleJobConstStatusModule(2))

	err := runWork(t, w, jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType, TenantID: tenantID})
	if _, ok := errors.AsType[*river.JobCancelError](err); !ok {
		t.Fatalf("expected a river.JobCancelError for a permanent status, got %v (%T)", err, err)
	}
}

func TestNewJobEnvelope_CarriesRiverJobMetadata(t *testing.T) {
	job := &river.Job[jobqueue.WASMJobArgs]{
		JobRow: &rivertype.JobRow{ID: 42, Attempt: 2, MaxAttempts: 5},
		Args: jobqueue.WASMJobArgs{
			ModuleName: testModuleName, JobType: testJobType, TenantID: "tenant-1",
			TraceID: "trace-1", Payload: []byte("p"),
		},
	}

	got := newJobEnvelope(job)
	want := abiv1.JobEnvelope{
		JobID: "job_42", JobType: testJobType, TenantID: "tenant-1", ModuleName: testModuleName,
		TraceID: "trace-1", Attempt: 2, MaxAttempts: 5, Payload: []byte("p"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newJobEnvelope() = %+v, want %+v", got, want)
	}
}

func TestWork_TrapReturnsError(t *testing.T) {
	w, tenantID := newTestWorker(t, handleJobTrapsModule)

	err := runWork(t, w, jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType, TenantID: tenantID})
	if err == nil {
		t.Fatal("expected an error from a handler that traps")
	}
}

func TestWork_MissingHandleJobExportReturnsError(t *testing.T) {
	w, tenantID := newTestWorker(t, getDataModule)

	err := runWork(t, w, jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType, TenantID: tenantID})
	if err == nil {
		t.Fatal("expected an error when the module has no handle_job export")
	}
}

func TestWork_UnknownModuleReturnsError(t *testing.T) {
	w, _ := newTestWorker(t, buildHandleJobConstStatusModule(0))

	err := runWork(t, w, jobqueue.WASMJobArgs{ModuleName: "does-not-exist", JobType: testJobType})
	if err == nil {
		t.Fatal("expected an error for an unknown module")
	}
}

// TestWork_NilPoolReturnsErrorNotPanic guards against a module manifest
// that legitimately declares wasm: false (manifest/module_type.go's
// "theme" type requires exactly that) reaching StatusReady with a nil
// Pool — job_types on such a module is a real, currently-unvalidated
// manifest inconsistency; Work must return an error, not panic on
// mod.Pool.Borrow.
func TestWork_NilPoolReturnsErrorNotPanic(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		testModuleName: {
			Status: module.StatusReady,
			Pool:   nil,
			Manifest: manifest.Manifest{
				Type:     "standard",
				JobTypes: []manifest.JobType{{Name: testJobType, Handler: "handle_test_job"}},
			},
		},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}
	w := &Worker{ModuleRegistry: reg}

	err := runWork(t, w, jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType})
	if err == nil {
		t.Fatal("expected an error for a module with a nil Pool")
	}
}

func TestWork_JobTypeNotOwnedByModuleReturnsError(t *testing.T) {
	w, _ := newTestWorker(t, buildHandleJobConstStatusModule(0))

	err := runWork(t, w, jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: "not_a_declared_job_type"})
	if err == nil {
		t.Fatal("expected an error for a job type not owned by the target module")
	}
}
