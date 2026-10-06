// Package jobs is the module-side caller for the host.jobs namespace
// (host-abi-reference.md §10, go-sdk-reference.md §9). A job type is declared
// once with Define, whose Def has Enqueue and EnqueueTx methods that queue a
// background job of one of the module's own declared job_types;
// EnqueueProvider, EnqueueProviderTx and DispatchProviderSync route a
// provider-category job (sms_send, payment_charge, ...) to a connector module
// (connector-guide.md §7); SetResult answers a DispatchProviderSync caller
// from inside the handler. The definitions live in the host-call-free package
// sdk/go/jobs/def so a module's schema package can name them; importing this
// package installs the host calls behind their enqueue methods.
package jobs

import (
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/djangbahevans/goerp/sdk/go/jobs/def"
	"github.com/vmihailenco/msgpack/v5"
)

func init() { def.SetEnqueuer(hostEnqueuer{}) }

// hostEnqueuer performs the enqueue host calls behind def.Def's methods.
type hostEnqueuer struct{}

func (hostEnqueuer) Enqueue(in abi.JobsEnqueueInput) (string, error) {
	var out abi.JobsEnqueueOutput
	if err := hostcall.Do(hostJobsEnqueue, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

func (hostEnqueuer) EnqueueTx(in abi.JobsEnqueueTxInput) (string, error) {
	var out abi.JobsEnqueueOutput
	if err := hostcall.Do(hostJobsEnqueueTx, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

// Queue names a job may run on.
const (
	QueueCritical = def.QueueCritical
	QueueDefault  = def.QueueDefault
	QueueBulk     = def.QueueBulk
	QueueEmail    = def.QueueEmail
	QueueSearch   = def.QueueSearch
)

// Def is a typed job definition (see def.Def).
type Def[P any] = def.Def[P]

// DefineOption configures Define.
type DefineOption = def.DefineOption

// JobOption configures an enqueue. An unset option falls back to the job
// definition, then to the job type's manifest declaration, then to the
// engine default.
type JobOption = def.JobOption

// Define declares a job type named name whose payload is a P.
func Define[P any](name string, opts ...DefineOption) Def[P] {
	return def.Define[P](name, opts...)
}

var (
	// Label sets the job's label in the admin UI. It is required.
	Label = def.Label
	// Description sets the job's admin UI description.
	Description = def.Description
	// Queue sets the queue the job runs on; unset runs on QueueDefault.
	Queue = def.Queue
	// Timeout sets the job's maximum execution time, up to 24 hours.
	Timeout = def.Timeout
	// MaxAttempts sets the job's retry attempts, up to 25.
	MaxAttempts = def.MaxAttempts
	// Priority sets the job's priority within its queue, 1-100.
	Priority = def.Priority
	// UniqueBy names the payload field, by its msgpack tag, whose value
	// deduplicates jobs.
	UniqueBy = def.UniqueBy

	// OnQueue runs the job on queue instead of its definition's queue.
	OnQueue = def.OnQueue
	// WithPriority sets the job's priority within its queue, 1-100.
	WithPriority = def.WithPriority
	// WithDelay runs the job no sooner than the duration from now. Cannot be
	// combined with ScheduleAt.
	WithDelay = def.WithDelay
	// ScheduleAt runs the job no sooner than the time, at one-second
	// precision. Cannot be combined with WithDelay.
	ScheduleAt = def.ScheduleAt
	// WithMaxAttempts overrides the definition's max attempts.
	WithMaxAttempts = def.WithMaxAttempts
	// WithIdempotencyKey deduplicates the job: enqueueing again with the
	// same key while a job with that key still exists returns the existing
	// job's ID instead of inserting a second one.
	WithIdempotencyKey = def.WithIdempotencyKey
)

// WithProviderModule sends an EnqueueProvider job to moduleName instead of
// the tenant's active provider for the category. The module must be
// installed, enabled and providing the category for the tenant. It is the
// only way to enqueue a multi-active category such as payment_provider.
func WithProviderModule(moduleName string) JobOption {
	return func(o *def.EnqueueOptions) { o.ProviderModule = moduleName }
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

	o := def.BuildOptions(opts)
	var out abi.JobsEnqueueProviderOutput
	in := abi.JobsEnqueueProviderInput{
		Category: category, JobType: jobType, Payload: data, ProviderModule: o.ProviderModule, Opts: o.Opts,
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

	o := def.BuildOptions(opts)
	var out abi.JobsEnqueueProviderOutput
	in := abi.JobsEnqueueProviderTxInput{
		TxID: tx.TxID(), Category: category, JobType: jobType, Payload: data, ProviderModule: o.ProviderModule, Opts: o.Opts,
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
