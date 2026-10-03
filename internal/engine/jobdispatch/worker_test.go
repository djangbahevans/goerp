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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
)

// getDataModule exports get_data (not handle_job) — enough to exercise
// missing-export error path.
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
// handle_job's body is an unconditional unreachable trap.
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

func newTestWorker(t *testing.T, wasmBytes []byte) (*Worker, string) {
	t.Helper()
	ctx := t.Context()

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
	return w.Work(t.Context(), &river.Job[jobqueue.WASMJobArgs]{JobRow: new(rivertype.JobRow{}), Args: args})
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
		JobRow: new(rivertype.JobRow{ID: 42, Attempt: 2, MaxAttempts: 5}),
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

func runRiverWork(t *testing.T, w *Worker, args jobqueue.WASMJobArgs) (*rivertest.WorkResult, error) {
	t.Helper()

	pool, err := pgxpool.New(t.Context(), jobsTestDSN)
	if err != nil {
		t.Fatalf("create job test pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := jobqueue.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("initialize job test schema: %v", err)
	}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin job test transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	tester := rivertest.NewWorker(t, riverpgxv5.New(pool), &river.Config{Schema: jobqueue.Schema}, w)
	args.MaxAttempts = 5
	return tester.Work(t.Context(), t, tx, args, nil)
}

func TestWork_InvalidJobCancelsAfterOneAttempt(t *testing.T) {
	for _, name := range []string{"nil_pool", "undeclared_job_type", "wrong_owner", "undeclared_migration", "removed_provider_category"} {
		t.Run(name, func(t *testing.T) {
			w, tenantID := newTestWorker(t, buildHandleJobConstStatusModule(0))
			mod := w.ModuleRegistry.Snapshot().Modules()[testModuleName]
			modules := map[string]*module.LoadedModule{testModuleName: mod}
			args := jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType, TenantID: tenantID}

			switch name {
			case "nil_pool":
				mod.Pool = nil
			case "undeclared_job_type":
				args.JobType = "not_a_declared_job_type"
			case "wrong_owner":
				mod.Manifest.JobTypes = nil
				modules["other_module"] = &module.LoadedModule{
					Status: module.StatusReady,
					Manifest: manifest.Manifest{
						Type:     "standard",
						JobTypes: []manifest.JobType{{Name: testJobType, Handler: "handle_test_job"}},
					},
				}
			case "undeclared_migration":
				args.IsDataMigration = true
			case "removed_provider_category":
				args.ProviderCategory = "payment_provider"
			}
			if _, err := w.ModuleRegistry.Update(modules); err != nil {
				t.Fatalf("update job registry: %v", err)
			}

			result, err := runRiverWork(t, w, args)
			if err != nil {
				t.Fatalf("work invalid job: %v", err)
			}
			if result.Job.State != rivertype.JobStateCancelled || result.Job.Attempt != 1 {
				t.Fatalf("state = %s, attempt = %d; want cancelled after one attempt", result.Job.State, result.Job.Attempt)
			}
		})
	}
}

func TestWork_TransientDispatchFailureRemainsRetryable(t *testing.T) {
	for _, name := range []string{"no_snapshot", "unknown_module", "module_not_ready", "borrow_failure", "tenant_lookup_failure"} {
		t.Run(name, func(t *testing.T) {
			w, tenantID := newTestWorker(t, buildHandleJobConstStatusModule(0))
			args := jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType, TenantID: tenantID}
			mod := w.ModuleRegistry.Snapshot().Modules()[testModuleName]

			switch name {
			case "no_snapshot":
				w.ModuleRegistry = &registry.ModuleRegistry{}
			case "unknown_module":
				args.ModuleName = "does-not-exist"
			case "module_not_ready":
				mod.Status = module.StatusWarming
				if _, err := w.ModuleRegistry.Update(map[string]*module.LoadedModule{testModuleName: mod}); err != nil {
					t.Fatalf("update warming module: %v", err)
				}
			case "borrow_failure":
				for range 2 {
					inst, err := mod.Pool.Borrow(t.Context())
					if err != nil {
						t.Fatalf("occupy job instance: %v", err)
					}
					t.Cleanup(func() { mod.Pool.Return(inst) })
				}
			case "tenant_lookup_failure":
				args.TenantID = "00000000-0000-0000-0000-000000000000"
			}

			result, err := runRiverWork(t, w, args)
			if err == nil {
				t.Fatal("expected a retryable dispatch error")
			}
			if _, cancelled := errors.AsType[*river.JobCancelError](err); cancelled {
				t.Fatalf("transient error cancelled the job: %v", err)
			}
			if (result.Job.State != rivertype.JobStateRetryable && result.Job.State != rivertype.JobStateAvailable) || result.Job.Attempt != 1 {
				t.Fatalf("state = %s, attempt = %d; want retryable after one attempt", result.Job.State, result.Job.Attempt)
			}
		})
	}
}

func TestSyncDispatcher_NilPoolReturnsTargetUnavailable(t *testing.T) {
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		testModuleName: {Status: module.StatusReady, Manifest: manifest.Manifest{Type: "standard"}},
	}); err != nil {
		t.Fatalf("update module registry: %v", err)
	}
	d := &SyncDispatcher{ModuleRegistry: reg}

	_, _, err := d.DispatchJobSync(t.Context(), wasm.SyncJobRequest{ModuleName: testModuleName})
	if !errors.Is(err, wasm.ErrSyncJobTargetUnavailable) {
		t.Fatalf("error = %v; want ErrSyncJobTargetUnavailable", err)
	}
	if _, cancelled := errors.AsType[*river.JobCancelError](err); cancelled {
		t.Fatalf("synchronous dispatch returned a River cancellation: %v", err)
	}
}
