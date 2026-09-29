package jobdispatch

import (
	"context"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// SyncDispatcher implements wasm.SyncJobDispatcher for
// host.jobs.dispatch_provider_sync (host-abi-reference.md §10): the target
// module's handle_job runs in-process, under the calling request's tenant
// and trace, with no River job inserted. It shares Worker.Work's
// invocation step (invokeHandleJob), so a provider's handler runs the same
// way on either path, and is injected via Runtime.SetSyncJobDispatcher for
// the same import-cycle reason eventdelivery.SyncDispatcher is.
type SyncDispatcher struct {
	ModuleRegistry *registry.ModuleRegistry
	Runtime        *wasm.Runtime
}

// DispatchJobSync runs req.ModuleName's handle_job and returns its status
// and the value it passed to host.jobs.set_result, if any. ctx carries the
// dispatch timeout; host.jobs.dispatch_provider_sync has already checked
// the module provides the category for the tenant.
func (d *SyncDispatcher) DispatchJobSync(ctx context.Context, req wasm.SyncJobRequest) (int32, []byte, error) {
	snap := d.ModuleRegistry.Snapshot()
	if snap == nil {
		return 0, nil, fmt.Errorf("%w: module registry has no snapshot yet", wasm.ErrSyncJobTargetUnavailable)
	}

	mod, err := readyModule(snap, req.ModuleName)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", wasm.ErrSyncJobTargetUnavailable, err)
	}

	args := jobqueue.WASMJobArgs{
		ModuleName: req.ModuleName,
		JobType:    req.JobType,
		Payload:    req.Payload,
		TenantID:   req.TenantID,
		TraceID:    req.TraceID,
	}
	// No River job exists, so the envelope has no job ID and a single
	// attempt.
	env := abiv1.JobEnvelope{
		JobType:     req.JobType,
		TenantID:    req.TenantID,
		ModuleName:  req.ModuleName,
		TraceID:     req.TraceID,
		Attempt:     1,
		MaxAttempts: 1,
		Payload:     req.Payload,
	}
	return invokeHandleJob(ctx, d.Runtime, snap, mod, args, req.TenantSlug, env, true)
}
