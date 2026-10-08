package jobdispatch

import (
	"context"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// Synchronous providers use the same live identity resolution as queued jobs.
type SyncDispatcher struct {
	ModuleRegistry *registry.ModuleRegistry
	Runtime        *wasm.Runtime
	Roles          *role.Store
}

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
		UserID:     req.UserID,
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
	return invokeHandleJob(ctx, d.Runtime, d.Roles, snap, mod, args, req.TenantSlug, env, true, nil)
}
