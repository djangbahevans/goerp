package wasm

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	maxJobPayloadBytes = 1 << 20

	// Defaults for a job type whose manifest declaration leaves them unset
	// (manifest-spec.md §15).
	defaultJobPriority    = 50
	defaultJobMaxAttempts = 3
)

// enqueueableQueues are the business queues a module may enqueue onto;
// the platform-reserved admin and events queues are excluded.
var enqueueableQueues = map[string]bool{
	jobqueue.QueueCritical: true,
	jobqueue.QueueDefault:  true,
	jobqueue.QueueBulk:     true,
	jobqueue.QueueEmail:    true,
	jobqueue.QueueSearch:   true,
}

// registerHostJobs attaches host.jobs.enqueue and host.jobs.enqueue_tx.
// insertClient is the never-started database/sql River client
// registerHostEvent also uses, so enqueue_tx can insert on the *sql.Tx a
// module opened through host.db.begin.
func registerHostJobs(ctx context.Context, rt wazero.Runtime, r *Runtime, insertClient *river.Client[*sql.Tx]) error {
	_, err := rt.NewHostModuleBuilder("host.jobs").
		NewFunctionBuilder().WithFunc(makeJobsEnqueue(r, insertClient)).Export("enqueue").
		NewFunctionBuilder().WithFunc(makeJobsEnqueueTx(r, insertClient)).Export("enqueue_tx").
		Instantiate(ctx)
	return err
}

func makeJobsEnqueue(r *Runtime, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
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
		var input abiv1.JobsEnqueueInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		args, opts, hostErr := buildJobInsert(modCtx, input.Type, input.Payload, input.Opts, time.Now())
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		res, err := insertClient.Insert(ctx, args, opts)
		return writeJobInsertResult(ctx, m, allocate, res, err)
	}
}

func makeJobsEnqueueTx(r *Runtime, insertClient *river.Client[*sql.Tx]) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
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
		var input abiv1.JobsEnqueueTxInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeTransactionNotFound, Message: "transaction ID does not exist or has expired",
			})
		}

		args, opts, hostErr := buildJobInsert(modCtx, input.Type, input.Payload, input.Opts, time.Now())
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		res, err := insertClient.InsertTx(ctx, tx, args, opts)
		return writeJobInsertResult(ctx, m, allocate, res, err)
	}
}

func writeJobInsertResult(ctx context.Context, m api.Module, allocate api.Function, res *rivertype.JobInsertResult, err error) uint64 {
	if err != nil {
		return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
			Code: abiv1.ErrCodeJobsQueueUnavailable, Message: err.Error(), Retry: true,
		})
	}
	return abi.WriteToModule(ctx, m, allocate, abiv1.JobsEnqueueOutput{
		JobID:        jobqueue.EncodeJobID(res.Job.ID),
		Deduplicated: res.UniqueSkippedAsDuplicate,
	})
}

// jobMetadata is stamped onto every module-enqueued River job's metadata,
// so the job's origin is visible without decoding its args.
type jobMetadata struct {
	TenantID   string `json:"tenant_id"`
	ModuleName string `json:"module_name"`
	TraceID    string `json:"trace_id,omitempty"`
}

// buildJobInsert validates one host.jobs enqueue against the calling
// module's own job_types and resolves its options, falling back from the
// caller's opts to the job type's manifest declaration to the engine
// defaults.
func buildJobInsert(modCtx *ModuleContext, jobType string, payload []byte, o abiv1.JobEnqueueOptions, now time.Time) (jobqueue.WASMJobArgs, *river.InsertOpts, *abiv1.HostError) {
	jt, ok := modCtx.JobType(jobType)
	if !ok {
		return jobqueue.WASMJobArgs{}, nil, &abiv1.HostError{
			Code:    abiv1.ErrCodeJobsUndeclaredType,
			Message: fmt.Sprintf("job type %q is not in this module's declared job_types", jobType),
		}
	}

	if len(payload) > maxJobPayloadBytes {
		return jobqueue.WASMJobArgs{}, nil, &abiv1.HostError{
			Code:    abiv1.ErrCodeJobsPayloadTooLarge,
			Message: fmt.Sprintf("job payload is %d bytes, over the %d-byte limit", len(payload), maxJobPayloadBytes),
		}
	}

	queue := cmp.Or(o.Queue, jt.Queue, jobqueue.QueueDefault)
	if !enqueueableQueues[queue] {
		return invalidJobOptions("queue %q is not one of critical, default, bulk, email, search", queue)
	}

	priority := cmp.Or(o.Priority, jt.Priority, defaultJobPriority)
	if priority < 1 || priority > 100 {
		return invalidJobOptions("priority %d is outside 1-100", priority)
	}

	if o.MaxAttempts < 0 {
		return invalidJobOptions("max_attempts %d is negative", o.MaxAttempts)
	}
	maxAttempts := cmp.Or(o.MaxAttempts, jt.MaxAttempts, defaultJobMaxAttempts)

	if o.DelayMs < 0 {
		return invalidJobOptions("delay_ms %d is negative", o.DelayMs)
	}
	if o.DelayMs > 0 && o.ScheduledAt != 0 {
		return invalidJobOptions("delay_ms and scheduled_at are mutually exclusive")
	}
	scheduledAt := now.Add(time.Duration(o.DelayMs) * time.Millisecond)
	if o.ScheduledAt != 0 {
		scheduledAt = time.Unix(o.ScheduledAt, 0)
	}

	idempotencyKey := o.IdempotencyKey
	if idempotencyKey == "" && jt.UniqueBy != "" {
		idempotencyKey, _ = extractPayloadField(payload, jt.UniqueBy)
	}

	metadata, err := json.Marshal(jobMetadata{TenantID: modCtx.TenantID, ModuleName: modCtx.ModuleName, TraceID: modCtx.TraceID})
	if err != nil {
		return jobqueue.WASMJobArgs{}, nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
	}

	args := jobqueue.WASMJobArgs{
		ModuleName:     modCtx.ModuleName,
		JobType:        jobType,
		Payload:        payload,
		TenantID:       modCtx.TenantID,
		Queue:          queue,
		MaxAttempts:    maxAttempts,
		IdempotencyKey: idempotencyKey,
		TraceID:        modCtx.TraceID,
	}
	opts := &river.InsertOpts{
		Queue:       queue,
		Priority:    riverPriority(priority),
		MaxAttempts: maxAttempts,
		ScheduledAt: scheduledAt,
		Metadata:    metadata,
	}
	if idempotencyKey != "" {
		opts.UniqueOpts = river.UniqueOpts{ByArgs: true}
	}
	return args, opts, nil
}

func invalidJobOptions(format string, a ...any) (jobqueue.WASMJobArgs, *river.InsertOpts, *abiv1.HostError) {
	return jobqueue.WASMJobArgs{}, nil, &abiv1.HostError{
		Code:    abiv1.ErrCodeJobsInvalidOptions,
		Message: fmt.Sprintf(format, a...),
	}
}

// riverPriority maps the ABI's 1-100 priority (higher runs sooner) onto
// River's 1-4 (1 runs soonest), in bands of 25.
func riverPriority(p int) int {
	return 4 - (p-1)/25
}
