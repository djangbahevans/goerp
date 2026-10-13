package jobdispatch

import (
	"errors"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func TestWork_QueuedVersionCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name     string
		pin      string
		boundary string
		cancel   bool
	}{
		{name: "untagged", boundary: ">= 2.0.0"},
		{name: "same version skips migrations", pin: "2.0.0", boundary: "invalid"},
		{name: "additive upgrade", pin: "1.0.0"},
		{name: "migration before pin", pin: "1.0.0", boundary: ">= 1.0.0"},
		{name: "migration after live version", pin: "1.0.0", boundary: ">= 3.0.0"},
		{name: "incompatible upgrade", pin: "1.0.0", boundary: ">= 2.0.0", cancel: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wasmBytes := buildHandleJobConstStatusModule(0)
			if tt.cancel {
				wasmBytes = handleJobTrapsModule
			}
			w, tenantID := newTestWorker(t, wasmBytes)
			mod := *w.ModuleRegistry.Snapshot().Modules()[testModuleName]
			mod.Manifest.Version = "2.0.0"
			if tt.boundary != "" {
				mod.DataMigrations = []model.DataMigration{{ToVersion: tt.boundary, Handler: "migrate"}}
			}
			if _, err := w.ModuleRegistry.Update(map[string]*module.LoadedModule{testModuleName: &mod}); err != nil {
				t.Fatal(err)
			}

			var tags []string
			if tt.pin != "" {
				tags = []string{jobqueue.ModuleVersionTag(tt.pin)}
			}
			err := w.Work(t.Context(), &river.Job[jobqueue.WASMJobArgs]{
				JobRow: new(rivertype.JobRow{Tags: tags}),
				Args:   jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType, TenantID: tenantID},
			})
			if tt.cancel {
				if _, cancelled := errors.AsType[*river.JobCancelError](err); !cancelled {
					t.Fatalf("Work() = %v, want permanent cancellation before the trapping handler", err)
				}
				if !strings.Contains(err.Error(), "1.0.0") || !strings.Contains(err.Error(), "2.0.0") {
					t.Fatalf("cancellation omits module versions: %v", err)
				}
			} else if err != nil {
				t.Fatalf("compatible job failed: %v", err)
			}
		})
	}
}

func TestWork_IncompatibleVersionCancelsAfterOneAttempt(t *testing.T) {
	w, tenantID := newTestWorker(t, handleJobTrapsModule)
	mod := *w.ModuleRegistry.Snapshot().Modules()[testModuleName]
	mod.Manifest.Version = "2.0.0"
	mod.DataMigrations = []model.DataMigration{{ToVersion: ">= 2.0.0", Handler: "migrate"}}
	if _, err := w.ModuleRegistry.Update(map[string]*module.LoadedModule{testModuleName: &mod}); err != nil {
		t.Fatal(err)
	}

	result, err := runRiverWork(t, w, jobqueue.WASMJobArgs{
		ModuleName: testModuleName,
		JobType:    testJobType,
		TenantID:   tenantID,
	}, &river.InsertOpts{Tags: []string{jobqueue.ModuleVersionTag("1.0.0")}})
	if err != nil {
		t.Fatalf("work incompatible job: %v", err)
	}
	if result.Job.State != rivertype.JobStateCancelled || result.Job.Attempt != 1 {
		t.Fatalf("state = %s, attempt = %d; want cancelled after one attempt", result.Job.State, result.Job.Attempt)
	}
}
