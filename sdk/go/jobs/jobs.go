// Package jobs is the module-side caller for the host.jobs namespace
// (host-abi-reference.md §10, go-sdk-reference.md §9). A job type is declared
// once with Define, whose Def has Enqueue and EnqueueTx methods that queue a
// background job of one of the module's own declared job_types;
// DefineProvider declares a provider-category job (sms_send,
// payment_charge, ...) whose ProviderDef routes Enqueue, EnqueueTx and
// DispatchSync to a connector module (connector-guide.md §7); SetResult
// answers a DispatchSync caller from inside the handler. The definitions live
// in the host-call-free package sdk/go/jobs/def so a module's schema package
// can name them; importing this package installs the host calls behind their
// enqueue methods.
package jobs

import (
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
	"github.com/djangbahevans/goerp/sdk/go/jobs/def"
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

func (hostEnqueuer) EnqueueProvider(in abi.JobsEnqueueProviderInput) (string, error) {
	var out abi.JobsEnqueueProviderOutput
	if err := hostcall.Do(hostJobsEnqueueProvider, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

func (hostEnqueuer) EnqueueProviderTx(in abi.JobsEnqueueProviderTxInput) (string, error) {
	var out abi.JobsEnqueueProviderOutput
	if err := hostcall.Do(hostJobsEnqueueProviderTx, in, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

func (hostEnqueuer) DispatchProviderSync(in abi.JobsDispatchProviderSyncInput) (abi.JobsDispatchProviderSyncOutput, error) {
	var out abi.JobsDispatchProviderSyncOutput
	err := hostcall.Do(hostJobsDispatchProviderSync, in, &out)
	return out, err
}

func (hostEnqueuer) SetProviderResult(in abi.JobsSetResultInput) error {
	return hostcall.Do(hostJobsSetResult, in, nil)
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

// Definition is the payload-type-erased view of a Def (see def.Definition).
type Definition = def.Definition

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

// WithProviderModule sends a ProviderDef's job to moduleName instead of the
// tenant's active provider for the category. The module must be installed,
// enabled and providing the category for the tenant. It is the only way to
// enqueue a multi-active category such as payment_provider.
func WithProviderModule(moduleName string) JobOption {
	return func(o *def.EnqueueOptions) { o.ProviderModule = moduleName }
}

// ProviderDef is a typed provider-category job definition (see
// def.ProviderDef).
type ProviderDef[P, R any] = def.ProviderDef[P, R]

// SyncOption configures one ProviderDef.DispatchSync.
type SyncOption = def.SyncOption

// DefineProvider declares the provider-category job jobType, which runs on a
// connector module providing category, whose payload is a P and whose
// synchronous result is an R.
func DefineProvider[P, R any](category, jobType string) ProviderDef[P, R] {
	return def.DefineProvider[P, R](category, jobType)
}

// WithSyncTimeout bounds how long DispatchSync waits for the handler,
// instead of the engine's GOERP_SYNC_PROVIDER_TIMEOUT default (15s).
func WithSyncTimeout(d time.Duration) SyncOption { return def.WithSyncTimeout(d) }


