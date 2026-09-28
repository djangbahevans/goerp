package wasm

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// defaultSyncProviderTimeout backs dispatch_provider_sync when neither the
// caller's timeout_ms nor config.SyncProviderTimeout sets one.
const defaultSyncProviderTimeout = 15 * time.Second

// ProviderStore answers which module handles a provider category for a
// tenant (connector-guide.md §7) — satisfied by *providerselect.Store.
type ProviderStore interface {
	Resolve(ctx context.Context, tenantID, category string) (string, error)
	IsEnabledProvider(ctx context.Context, tenantID, moduleName, category string) (bool, error)
}

// SyncJobRequest is one host.jobs.dispatch_provider_sync invocation of
// ModuleName's handle_job, run under the calling request's tenant and
// trace.
type SyncJobRequest struct {
	ModuleName string
	JobType    string
	Payload    []byte
	TenantID   string
	TenantSlug string
	TraceID    string
}

// ErrSyncJobTargetUnavailable marks a SyncJobDispatcher failure before the
// handler ran — the module isn't loaded and ready, or no instance could be
// borrowed — which a later call may not hit.
var ErrSyncJobTargetUnavailable = errors.New("target module unavailable")

// SyncJobDispatcher invokes another module's handle_job export in-process
// for host.jobs.dispatch_provider_sync, returning its status and whatever
// it passed to host.jobs.set_result. A failure before the handler ran wraps
// ErrSyncJobTargetUnavailable. Implemented by internal/engine/jobdispatch
// and injected via Runtime.SetSyncJobDispatcher, for the same import-cycle
// reason as SyncEventDispatcher. ctx already carries the dispatch timeout.
type SyncJobDispatcher interface {
	DispatchJobSync(ctx context.Context, req SyncJobRequest) (status int32, result []byte, err error)
}

// resolveProviderModule picks the module a provider-category job goes to:
// providerModule when given, validated as enabled and providing category
// for the tenant, else the tenant's single active provider. A multi-active
// category is never resolved — its caller must name the module.
func (r *Runtime) resolveProviderModule(ctx context.Context, modCtx *ModuleContext, category, providerModule string) (string, *abiv1.HostError) {
	if !providerselect.IsCategory(category) {
		return "", &abiv1.HostError{
			Code:    abiv1.ErrCodeJobsInvalidProviderCategory,
			Message: fmt.Sprintf("%q is not a provider category", category),
		}
	}
	if r.providerStore == nil {
		return "", &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "provider resolution is not available yet (engine still starting up)", Retry: true}
	}

	if providerModule != "" {
		ok, err := r.providerStore.IsEnabledProvider(ctx, modCtx.TenantID, providerModule, category)
		if err != nil {
			return "", &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
		}
		if !ok {
			return "", &abiv1.HostError{
				Code:    abiv1.ErrCodeJobsProviderModuleNotEnabled,
				Message: fmt.Sprintf("module %q is not installed, enabled and providing %s for this tenant", providerModule, category),
			}
		}
		return providerModule, nil
	}

	if providerselect.IsMultiActive(category) {
		return "", &abiv1.HostError{
			Code:    abiv1.ErrCodeJobsInvalidProviderCategory,
			Message: fmt.Sprintf("%s is multi-active: name the target module with provider_module", category),
		}
	}

	resolved, err := r.providerStore.Resolve(ctx, modCtx.TenantID, category)
	switch {
	case err == nil:
		return resolved, nil
	case errors.Is(err, providerselect.ErrNoProviderSelected):
		return "", &abiv1.HostError{Code: abiv1.ErrCodeJobsNoProviderSelected, Message: fmt.Sprintf("several modules provide %s and none is selected", category)}
	case errors.Is(err, providerselect.ErrNoProviderInstalled):
		return "", &abiv1.HostError{Code: abiv1.ErrCodeJobsNoProviderInstalled, Message: fmt.Sprintf("no enabled module provides %s", category)}
	default:
		return "", &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
}

// buildProviderJobInsert resolves a provider-category job's target module
// and builds its River insert.
func (r *Runtime) buildProviderJobInsert(ctx context.Context, modCtx *ModuleContext, category, jobType, providerModule string, payload []byte, o abiv1.JobEnqueueOptions) (jobqueue.WASMJobArgs, *river.InsertOpts, *abiv1.HostError) {
	if jobType == "" {
		return jobqueue.WASMJobArgs{}, nil, &abiv1.HostError{Code: abiv1.ErrCodeJobsInvalidOptions, Message: "job_type is required"}
	}
	target, hostErr := r.resolveProviderModule(ctx, modCtx, category, providerModule)
	if hostErr != nil {
		return jobqueue.WASMJobArgs{}, nil, hostErr
	}
	return buildJobInsertFor(modCtx, manifest.JobType{Name: jobType}, target, category, payload, o, time.Now())
}

func makeJobsEnqueueProvider(r *Runtime, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapJobsEnqueue) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("jobs.enqueue"))
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.JobsEnqueueProviderInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		args, opts, hostErr := r.buildProviderJobInsert(ctx, modCtx, input.Category, input.JobType, input.ProviderModule, input.Payload, input.Opts)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		res, err := insertClient.Insert(ctx, args, opts)
		return writeProviderJobInsertResult(ctx, m, allocate, args.ModuleName, res, err)
	}
}

func makeJobsEnqueueProviderTx(r *Runtime, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapJobsEnqueue) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("jobs.enqueue"))
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.JobsEnqueueProviderTxInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeTransactionNotFound, Message: "transaction ID does not exist or has expired",
			})
		}

		args, opts, hostErr := r.buildProviderJobInsert(ctx, modCtx, input.Category, input.JobType, input.ProviderModule, input.Payload, input.Opts)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		res, err := insertClient.InsertTx(ctx, tx, args, opts)
		return writeProviderJobInsertResult(ctx, m, allocate, args.ModuleName, res, err)
	}
}

func writeProviderJobInsertResult(ctx context.Context, m api.Module, allocate api.Function, resolvedModule string, res *rivertype.JobInsertResult, err error) uint64 {
	if err != nil {
		return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
			Code: abiv1.ErrCodeJobsQueueUnavailable, Message: err.Error(), Retry: true,
		})
	}
	return abi.WriteToModule(ctx, m, allocate, abiv1.JobsEnqueueProviderOutput{
		JobID:          jobqueue.EncodeJobID(res.Job.ID),
		Deduplicated:   res.UniqueSkippedAsDuplicate,
		ResolvedModule: resolvedModule,
	})
}

// makeJobsDispatchProviderSync builds host.jobs.dispatch_provider_sync:
// invoke provider_module's handle_job in-process and block for its
// host.jobs.set_result value, with no River job inserted. Like
// host.event.emit's sync path it is refused while the caller holds a
// host.db transaction open, and a failure is returned to the caller as is.
func makeJobsDispatchProviderSync(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapJobsEnqueue) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("jobs.enqueue"))
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.JobsDispatchProviderSyncInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		timeout, hostErr := r.validateSyncProviderDispatch(modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		target, hostErr := r.resolveProviderModule(ctx, modCtx, input.Category, input.ProviderModule)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		if r.syncJobDispatcher == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: "synchronous job dispatch is not available yet (engine still starting up)", Retry: true,
			})
		}

		dispatchCtx, cancel := context.WithTimeout(ctx, timeout)
		status, result, err := r.syncJobDispatcher.DispatchJobSync(dispatchCtx, SyncJobRequest{
			ModuleName: target, JobType: input.JobType, Payload: input.Payload,
			TenantID: modCtx.TenantID, TenantSlug: modCtx.TenantSlug, TraceID: modCtx.TraceID,
		})
		// Only a call that failed counts as timed out: a handler that
		// returned just as the deadline passed has still run to completion,
		// and reporting that as a retryable timeout would invite a repeat.
		timedOut := err != nil && errors.Is(dispatchCtx.Err(), context.DeadlineExceeded)
		cancel()

		switch {
		case timedOut:
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeJobsSyncDispatchTimeout,
				Message: fmt.Sprintf("%s did not handle %s within %s", target, input.JobType, timeout),
				Retry:   true,
			})
		case errors.Is(err, ErrSyncJobTargetUnavailable):
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true,
			})
		case err != nil:
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeJobsHandlerFailed, Message: fmt.Sprintf("%s: %v", target, err),
			})
		case status != 0:
			// 2 is a permanent failure, as for handle_event
			// (ModuleInstance.InvokeHandleEvent); anything else may succeed
			// if the caller tries again.
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeJobsHandlerFailed,
				Message: fmt.Sprintf("%s's %s handler returned status %d", target, input.JobType, status),
				Retry:   status != 2,
			})
		}

		return abi.WriteToModule(ctx, m, allocate, abiv1.JobsDispatchProviderSyncOutput{ResultPayload: result})
	}
}

// validateSyncProviderDispatch checks a dispatch_provider_sync request
// before any provider lookup, and returns its timeout.
func (r *Runtime) validateSyncProviderDispatch(modCtx *ModuleContext, input abiv1.JobsDispatchProviderSyncInput) (time.Duration, *abiv1.HostError) {
	if len(modCtx.TransactionIDs()) > 0 {
		return 0, &abiv1.HostError{
			Code:    abiv1.ErrCodeJobsSyncInTransaction,
			Message: "dispatch_provider_sync cannot be called with a transaction open — commit or roll it back first",
		}
	}
	if input.ProviderModule == "" {
		return 0, &abiv1.HostError{Code: abiv1.ErrCodeJobsProviderModuleNotEnabled, Message: "provider_module is required"}
	}
	if input.JobType == "" {
		return 0, &abiv1.HostError{Code: abiv1.ErrCodeJobsInvalidOptions, Message: "job_type is required"}
	}
	if input.TimeoutMs < 0 {
		return 0, &abiv1.HostError{Code: abiv1.ErrCodeJobsInvalidOptions, Message: fmt.Sprintf("timeout_ms %d is negative", input.TimeoutMs)}
	}
	if len(input.Payload) > maxJobPayloadBytes {
		return 0, &abiv1.HostError{
			Code:    abiv1.ErrCodeJobsPayloadTooLarge,
			Message: fmt.Sprintf("job payload is %d bytes, over the %d-byte limit", len(input.Payload), maxJobPayloadBytes),
		}
	}

	// Clamped so a huge timeout_ms can't overflow into an instant deadline;
	// the caller's own request context still bounds the dispatch.
	timeoutMs := min(input.TimeoutMs, math.MaxInt64/int64(time.Millisecond))
	return cmp.Or(time.Duration(timeoutMs)*time.Millisecond, r.syncProviderTimeout, defaultSyncProviderTimeout), nil
}

// makeJobsSetResult builds host.jobs.set_result: record the value a
// handler hands back to its host.jobs.dispatch_provider_sync caller. A
// handler run any other way (a River job, the async provider path) has no
// caller waiting, so the value is silently dropped.
func makeJobsSetResult(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.JobsSetResultInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}
		if len(input.Value) > maxJobPayloadBytes {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeJobsPayloadTooLarge,
				Message: fmt.Sprintf("job result is %d bytes, over the %d-byte limit", len(input.Value), maxJobPayloadBytes),
			})
		}

		modCtx.setJobResult(input.Value)
		return abi.WriteToModule(ctx, m, allocate, abiv1.JobsSetResultOutput{})
	}
}
