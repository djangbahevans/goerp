package def

import (
	"fmt"
	"reflect"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

// SyncOption configures one ProviderDef.DispatchSync.
type SyncOption func(*abi.JobsDispatchProviderSyncInput)

// WithSyncTimeout bounds how long DispatchSync waits for the handler,
// instead of the engine's GOERP_SYNC_PROVIDER_TIMEOUT default (15s).
func WithSyncTimeout(d time.Duration) SyncOption {
	return func(in *abi.JobsDispatchProviderSyncInput) { in.TimeoutMs = d.Milliseconds() }
}

// ProviderDef is a typed provider-category job definition binding a
// category, a platform-owned job type, the payload type P a caller sends and
// the result type R a connector hands back to a synchronous caller.
type ProviderDef[P, R any] struct {
	category string
	jobType  string
}

// DefineProvider declares the provider-category job jobType, which runs on a
// connector module providing category, called in init() or a package-level
// var. It panics when category or jobType is not snake_case.
func DefineProvider[P, R any](category, jobType string) ProviderDef[P, R] {
	if !namePattern.MatchString(category) {
		panic(fmt.Sprintf("jobs.DefineProvider: category %q must be snake_case", category))
	}
	if !namePattern.MatchString(jobType) {
		panic(fmt.Sprintf("jobs.DefineProvider: job type %q must be snake_case", jobType))
	}
	return ProviderDef[P, R]{category: category, jobType: jobType}
}

// Name returns the provider job's type name.
func (d ProviderDef[P, R]) Name() string { return d.jobType }

// Category returns the provider category the job runs on.
func (d ProviderDef[P, R]) Category() string { return d.category }

// PayloadType returns the reflect.Type of the job's payload type P.
func (d ProviderDef[P, R]) PayloadType() reflect.Type { return reflect.TypeFor[P]() }

// Enqueue queues the job on the tenant's active provider for the category,
// or on the module WithProviderModule names. Returns the job's ID.
func (d ProviderDef[P, R]) Enqueue(payload P, opts ...JobOption) (string, error) {
	if enqueuer == nil {
		return "", ErrNoEnqueuer
	}
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}
	o := BuildOptions(opts)
	return enqueuer.EnqueueProvider(abi.JobsEnqueueProviderInput{
		Category: d.category, JobType: d.jobType, Payload: data, ProviderModule: o.ProviderModule, Opts: o.Opts,
	})
}

// EnqueueTx is Enqueue inside tx. The job becomes visible to workers only if
// tx commits.
func (d ProviderDef[P, R]) EnqueueTx(tx Tx, payload P, opts ...JobOption) (string, error) {
	if enqueuer == nil {
		return "", ErrNoEnqueuer
	}
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return "", err
	}
	o := BuildOptions(opts)
	return enqueuer.EnqueueProviderTx(abi.JobsEnqueueProviderTxInput{
		TxID: tx.TxID(), Category: d.category, JobType: d.jobType, Payload: data, ProviderModule: o.ProviderModule, Opts: o.Opts,
	})
}

// DispatchSync runs moduleName's handler in-process and waits for it, with
// no job queued and so no retry. The result is the value the handler passed
// to SetResult, or R's zero value when it set none. Must not be called with
// a transaction open.
func (d ProviderDef[P, R]) DispatchSync(moduleName string, payload P, opts ...SyncOption) (R, error) {
	var result R
	if enqueuer == nil {
		return result, ErrNoEnqueuer
	}
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return result, err
	}

	in := abi.JobsDispatchProviderSyncInput{Category: d.category, ProviderModule: moduleName, JobType: d.jobType, Payload: data}
	for _, opt := range opts {
		opt(&in)
	}
	out, err := enqueuer.DispatchProviderSync(in)
	if err != nil {
		return result, err
	}
	if len(out.ResultPayload) == 0 {
		return result, nil
	}
	if err := msgpack.Unmarshal(out.ResultPayload, &result); err != nil {
		return result, fmt.Errorf("decode %s result: %w", d.jobType, err)
	}
	return result, nil
}
