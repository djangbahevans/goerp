// Package jobdispatch implements Worker, the River worker that processes
// jobqueue.WASMJobArgs jobs (manifest-spec.md §26, goerp#110) by invoking
// the target module's handle_job WASM export. It lives in its own
// package, separate from internal/engine/jobqueue itself, for the same
// import-cycle reason internal/engine/eventdelivery does (see that
// package's own doc comment): it needs internal/engine/registry (to
// resolve a *module.LoadedModule by name) and internal/engine/wasm, and
// internal/engine/wasm already imports internal/engine/jobqueue, so
// registry (which reaches wasm via internal/engine/module) can never be
// imported back into jobqueue without a cycle.
package jobdispatch

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/riverqueue/river"
)

// Worker processes jobqueue.WASMJobArgs jobs: resolve the target module,
// borrow an instance from its InstancePool, call InvokeHandleJob, and
// translate the result onto River's own retry semantics — a non-zero
// status or a Go/trap-level error both return a non-nil error from Work,
// so River retries per the job's own MaxAttempts (set at insert time,
// jobqueue.WASMJobArgs.InsertOpts) and marks it discarded once exhausted,
// never silently drops it. Doesn't itself build any richer per-job-type
// backoff/snooze policy — that's event-subscriber delivery's own
// retry_policy work (goerp#129), generalized to ordinary jobs by
// implementation-backlog.md #165, not this package's scope.
type Worker struct {
	river.WorkerDefaults[jobqueue.WASMJobArgs]
	ModuleRegistry *registry.ModuleRegistry
}

func (w *Worker) Work(ctx context.Context, job *river.Job[jobqueue.WASMJobArgs]) error {
	args := job.Args

	snap := w.ModuleRegistry.Snapshot()
	if snap == nil {
		// Shouldn't happen once the engine has started successfully
		// (ModuleRegistry.Update runs during Stage 3, well before any
		// worker can process a job) — guarded anyway, matching
		// eventdelivery.Worker's own nil-snapshot check. Returning an
		// error lets River retry once the registry is populated.
		return fmt.Errorf("module registry has no snapshot yet")
	}

	if owner, ok := snap.JobRegistry().Owner(args.JobType); !ok || owner != args.ModuleName {
		// A job whose declared (ModuleName, JobType) pair no longer
		// matches a live manifest declaration — a stale job surviving a
		// module removal/rename, or simply a caller-constructed args
		// value that named the wrong module for a real job type. Neither
		// is retryable: the mismatch won't resolve itself on a later
		// attempt.
		return fmt.Errorf("job type %q is not registered to module %q", args.JobType, args.ModuleName)
	}

	mod, ok := snap.Modules()[args.ModuleName]
	if !ok || mod.Status != module.StatusReady {
		return fmt.Errorf("module %q is not ready", args.ModuleName)
	}
	if mod.Pool == nil {
		// A module manifest can legitimately declare wasm: false (e.g.
		// the "theme" type requires it, manifest/module_type.go) and
		// still reach StatusReady with no compiled WASM at all — job_types
		// on such a module would be a manifest inconsistency nothing
		// today rejects at load time, but this must not panic on
		// mod.Pool.Borrow if it ever happens; not retryable, the mismatch
		// won't resolve itself on a later attempt.
		return fmt.Errorf("module %q has no WASM instance pool (wasm: false)", args.ModuleName)
	}

	inst, err := mod.Pool.Borrow(ctx)
	if err != nil {
		return fmt.Errorf("borrow instance for %s: %w", args.ModuleName, err)
	}
	defer mod.Pool.Return(inst)

	status, err := inst.InvokeHandleJob(ctx, args.Payload)
	if err != nil {
		return fmt.Errorf("invoke handle_job for %s/%s: %w", args.ModuleName, args.JobType, err)
	}
	if status != 0 {
		return fmt.Errorf("handle_job for %s/%s returned status %d", args.ModuleName, args.JobType, status)
	}

	return nil
}
