// Package jobs is the module-side caller for the host.jobs namespace
// (host-abi-reference.md §10): Enqueue and EnqueueTx queue a background
// job of one of the module's own declared job_types; EnqueueProvider,
// EnqueueProviderTx and DispatchProviderSync route a provider-category job
// (sms_send, payment_charge, ...) to a connector module
// (connector-guide.md §7); SetResult answers a DispatchProviderSync caller
// from inside the handler.
package jobs

import (
	"errors"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/vmihailenco/msgpack/v5"
)

// Queue names a job may be enqueued onto.
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueBulk     = "bulk"
	QueueEmail    = "email"
	QueueSearch   = "search"
)

// JobOption configures Enqueue/EnqueueTx/EnqueueProvider/EnqueueProviderTx.
// An unset option falls back to the job type's manifest declaration, then
// to the engine default.
type JobOption func(*enqueueOptions)

type enqueueOptions struct {
	opts           abi.JobEnqueueOptions
	providerModule string
}

// errProviderModuleOption rejects WithProviderModule on Enqueue/EnqueueTx,
// which only ever run the calling module's own job types.
var errProviderModuleOption = errors.New("jobs: WithProviderModule applies only to EnqueueProvider and EnqueueProviderTx")

// OnQueue runs the job on queue instead of its manifest-declared queue.
func OnQueue(queue string) JobOption {
	return func(o *enqueueOptions) { o.opts.Queue = queue }
}

// WithPriority sets the job's priority within its queue, 1-100, higher
// runs sooner.
func WithPriority(p int) JobOption {
	return func(o *enqueueOptions) { o.opts.Priority = p }
}

// WithDelay runs the job no sooner than d from now. Cannot be combined
// with ScheduleAt.
func WithDelay(d time.Duration) JobOption {
	return func(o *enqueueOptions) { o.opts.DelayMs = d.Milliseconds() }
}

// ScheduleAt runs the job no sooner than t, at one-second precision.
// Cannot be combined with WithDelay.
func ScheduleAt(t time.Time) JobOption {
	return func(o *enqueueOptions) { o.opts.ScheduledAt = t.Unix() }
}

// WithMaxAttempts overrides the job type's manifest-declared max_attempts.
func WithMaxAttempts(n int) JobOption {
	return func(o *enqueueOptions) { o.opts.MaxAttempts = n }
}

// WithIdempotencyKey deduplicates the job: enqueueing again with the same
// key while a job with that key still exists returns the existing job's
// ID instead of inserting a second one.
func WithIdempotencyKey(key string) JobOption {
	return func(o *enqueueOptions) { o.opts.IdempotencyKey = key }
}

// WithProviderModule sends an EnqueueProvider job to moduleName instead of
// the tenant's active provider for the category. The module must be
// installed, enabled and providing the category for the tenant. It is the
// only way to enqueue a multi-active category such as payment_provider.
func WithProviderModule(moduleName string) JobOption {
	return func(o *enqueueOptions) { o.providerModule = moduleName }
}

func buildOptions(opts []JobOption) enqueueOptions {
	var o enqueueOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Enqueue queues a jobType job via host.jobs.enqueue, msgpack-encoding
// payload as the job's payload. Returns the job's ID.
func Enqueue(jobType string, payload any, opts ...JobOption) (string, error) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}

	o := buildOptions(opts)
	if o.providerModule != "" {
		return "", errProviderModuleOption
	}

	var out abi.JobsEnqueueOutput
	in := abi.JobsEnqueueInput{Type: jobType, Payload: data, Opts: o.opts}
	if err := hostcall.Do(hostJobsEnqueue, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

// EnqueueTx queues a jobType job inside tx via host.jobs.enqueue_tx. The
// job becomes visible to workers only if tx commits.
func EnqueueTx(tx *db.Tx, jobType string, payload any, opts ...JobOption) (string, error) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}

	o := buildOptions(opts)
	if o.providerModule != "" {
		return "", errProviderModuleOption
	}

	var out abi.JobsEnqueueOutput
	in := abi.JobsEnqueueTxInput{TxID: tx.TxID(), Type: jobType, Payload: data, Opts: o.opts}
	if err := hostcall.Do(hostJobsEnqueueTx, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

// EnqueueProvider queues a provider-category job via
// host.jobs.enqueue_provider: jobType (e.g. "sms_send") runs on the
// tenant's active provider module for category (e.g. "sms_provider"), or on
// the module WithProviderModule names. Returns the job's ID.
func EnqueueProvider(category, jobType string, payload any, opts ...JobOption) (string, error) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}

	o := buildOptions(opts)
	var out abi.JobsEnqueueProviderOutput
	in := abi.JobsEnqueueProviderInput{
		Category: category, JobType: jobType, Payload: data, ProviderModule: o.providerModule, Opts: o.opts,
	}
	if err := hostcall.Do(hostJobsEnqueueProvider, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

// EnqueueProviderTx is EnqueueProvider inside tx via
// host.jobs.enqueue_provider_tx. The job becomes visible to workers only if
// tx commits.
func EnqueueProviderTx(tx *db.Tx, category, jobType string, payload any, opts ...JobOption) (string, error) {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}

	o := buildOptions(opts)
	var out abi.JobsEnqueueProviderOutput
	in := abi.JobsEnqueueProviderTxInput{
		TxID: tx.TxID(), Category: category, JobType: jobType, Payload: data, ProviderModule: o.providerModule, Opts: o.opts,
	}
	if err := hostcall.Do(hostJobsEnqueueProviderTx, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

// SyncOption configures DispatchProviderSync.
type SyncOption func(*abi.JobsDispatchProviderSyncInput)

// WithSyncTimeout bounds how long DispatchProviderSync waits for the
// handler, instead of the engine's GOERP_SYNC_PROVIDER_TIMEOUT default
// (15s).
func WithSyncTimeout(d time.Duration) SyncOption {
	return func(in *abi.JobsDispatchProviderSyncInput) { in.TimeoutMs = d.Milliseconds() }
}

// DispatchProviderSync runs moduleName's jobType handler in-process via
// host.jobs.dispatch_provider_sync and waits for it, with no job queued and
// so no retry. result must be a pointer; it is decoded from the value the
// handler passed to SetResult, and left untouched when the handler set
// none. Must not be called with a transaction open.
func DispatchProviderSync(category, moduleName, jobType string, payload, result any, opts ...SyncOption) error {
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return err
	}

	in := abi.JobsDispatchProviderSyncInput{Category: category, ProviderModule: moduleName, JobType: jobType, Payload: data}
	for _, opt := range opts {
		opt(&in)
	}

	var out abi.JobsDispatchProviderSyncOutput
	if err := hostcall.Do(hostJobsDispatchProviderSync, in, &out); err != nil {
		return err
	}
	if len(out.ResultPayload) == 0 || result == nil {
		return nil
	}
	return msgpack.Unmarshal(out.ResultPayload, result)
}

// SetResult hands v back to the DispatchProviderSync caller waiting on the
// running job handler, via host.jobs.set_result. It is a no-op when the
// handler is running as a queued job, since no caller is waiting.
func SetResult(v any) error {
	data, err := msgpack.Marshal(v)
	if err != nil {
		return err
	}
	return hostcall.Do(hostJobsSetResult, abi.JobsSetResultInput{Value: data}, nil)
}
