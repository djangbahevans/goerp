// Package def holds the host-call-free half of the jobs SDK: typed job
// definitions, their options and the per-enqueue options. A module's schema
// package imports it to name a job without linking host functions
// (go-sdk-reference.md §9 "Defining a job", §22 "Package layout"). The Def
// enqueue methods delegate to an Enqueuer that sdk/go/jobs installs when it
// is linked.
package def

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

// Queue names a job may run on.
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueBulk     = "bulk"
	QueueEmail    = "email"
	QueueSearch   = "search"
)

var queues = []string{QueueCritical, QueueDefault, QueueBulk, QueueEmail, QueueSearch}

// Limits the manifest allows on a job type (manifest-spec.md §15).
const (
	maxTimeout     = 24 * time.Hour
	maxMaxAttempts = 25
	minPriority    = 1
	maxPriority    = 100
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ErrNoEnqueuer is returned by a Def enqueue method when no Enqueuer is
// installed, i.e. sdk/go/jobs is not linked into the binary.
var ErrNoEnqueuer = errors.New("sdk/go/jobs/def: no enqueuer installed; import sdk/go/jobs to enqueue jobs")

// ErrProviderModuleOption rejects a provider-module option on a Def's
// Enqueue or EnqueueTx, which only ever run the calling module's own job
// types.
var ErrProviderModuleOption = errors.New("jobs: WithProviderModule applies only to a ProviderDef")

// Tx identifies an open database transaction. *db.Tx satisfies it.
type Tx interface {
	TxID() string
}

// Enqueuer performs the host calls behind the Def and ProviderDef methods.
type Enqueuer interface {
	Enqueue(in abi.JobsEnqueueInput) (string, error)
	EnqueueTx(in abi.JobsEnqueueTxInput) (string, error)
	EnqueueProvider(in abi.JobsEnqueueProviderInput) (string, error)
	EnqueueProviderTx(in abi.JobsEnqueueProviderTxInput) (string, error)
	DispatchProviderSync(in abi.JobsDispatchProviderSyncInput) (abi.JobsDispatchProviderSyncOutput, error)
}

var enqueuer Enqueuer

// SetEnqueuer installs the Enqueuer the Def enqueue methods delegate to. It
// is called from sdk/go/jobs's init.
func SetEnqueuer(e Enqueuer) { enqueuer = e }

// EnqueueOptions is the result of applying per-enqueue JobOptions.
type EnqueueOptions struct {
	Opts           abi.JobEnqueueOptions
	ProviderModule string
}

// JobOption configures one enqueue. An unset option falls back to the job
// definition, then to the job type's manifest declaration, then to the
// engine default.
type JobOption func(*EnqueueOptions)

// OnQueue runs the job on queue instead of its definition's queue.
func OnQueue(queue string) JobOption {
	return func(o *EnqueueOptions) { o.Opts.Queue = queue }
}

// WithPriority sets the job's priority within its queue, 1-100, higher runs
// sooner.
func WithPriority(p int) JobOption {
	return func(o *EnqueueOptions) { o.Opts.Priority = p }
}

// WithDelay runs the job no sooner than d from now. Cannot be combined with
// ScheduleAt.
func WithDelay(d time.Duration) JobOption {
	return func(o *EnqueueOptions) { o.Opts.DelayMs = d.Milliseconds() }
}

// ScheduleAt runs the job no sooner than t, at one-second precision. Cannot
// be combined with WithDelay.
func ScheduleAt(t time.Time) JobOption {
	return func(o *EnqueueOptions) { o.Opts.ScheduledAt = t.Unix() }
}

// WithMaxAttempts overrides the definition's max attempts.
func WithMaxAttempts(n int) JobOption {
	return func(o *EnqueueOptions) { o.Opts.MaxAttempts = n }
}

// WithIdempotencyKey deduplicates the job: enqueueing again with the same key
// while a job with that key still exists returns the existing job's ID
// instead of inserting a second one.
func WithIdempotencyKey(key string) JobOption {
	return func(o *EnqueueOptions) { o.Opts.IdempotencyKey = key }
}

// BuildOptions applies opts to a zero EnqueueOptions.
func BuildOptions(opts []JobOption) EnqueueOptions {
	var o EnqueueOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Defaults of a cron definition, which the engine runs without an enqueuer
// to supply them (manifest-spec.md §16).
const (
	cronDefaultTimeout = time.Hour
	cronDefaultQueue   = QueueBulk
)

// Spec is the execution defaults and admin metadata a definition carries. A
// zero Queue, Timeout, MaxAttempts or Priority is unset. Schedule and
// DisabledByDefault apply to cron definitions only; MaxAttempts, Priority and
// UniqueBy to job definitions only.
type Spec struct {
	Label             string
	Description       string
	Queue             string
	Timeout           time.Duration
	MaxAttempts       int
	Priority          int
	UniqueBy          string
	Schedule          string
	DisabledByDefault bool
}

// DefineOption configures Define.
type DefineOption func(*Spec)

// Label sets the job's label in the admin UI. It is required.
func Label(text string) DefineOption { return func(s *Spec) { s.Label = text } }

// Description sets the job's admin UI description.
func Description(text string) DefineOption { return func(s *Spec) { s.Description = text } }

// Queue sets the queue the job runs on; unset runs on QueueDefault.
func Queue(queue string) DefineOption { return func(s *Spec) { s.Queue = queue } }

// Timeout sets the job's maximum execution time, up to 24 hours; unset is
// five minutes.
func Timeout(d time.Duration) DefineOption { return func(s *Spec) { s.Timeout = d } }

// MaxAttempts sets the job's retry attempts, up to 25; unset is three.
func MaxAttempts(n int) DefineOption { return func(s *Spec) { s.MaxAttempts = n } }

// Priority sets the job's priority within its queue, 1-100, higher runs
// first; unset is 50.
func Priority(p int) DefineOption { return func(s *Spec) { s.Priority = p } }

// UniqueBy names the payload field, by its msgpack tag, whose value
// deduplicates jobs.
func UniqueBy(payloadField string) DefineOption {
	return func(s *Spec) { s.UniqueBy = payloadField }
}

// Schedule sets a cron definition's 5-field cron expression (minute hour day
// month weekday), in UTC. It is required by DefineCron and rejected by
// Define.
func Schedule(expr string) DefineOption { return func(s *Spec) { s.Schedule = expr } }

// DisabledByDefault leaves a cron definition off until a tenant admin enables
// it; unset runs from install. Rejected by Define.
func DisabledByDefault() DefineOption { return func(s *Spec) { s.DisabledByDefault = true } }

// Def is a typed job definition binding a job type's name, execution
// defaults and payload type P.
type Def[P any] struct {
	name string
	spec Spec
}

// Define declares a job type named name whose payload is a P, called in
// init() or a package-level var. It panics when name is not snake_case, no
// Label is given, an option is out of range, or an option that applies only
// to cron definitions (Schedule, DisabledByDefault) is given.
func Define[P any](name string, opts ...DefineOption) Def[P] {
	spec := applyDefineOptions(opts)
	validateCommon("jobs.Define", name, spec)

	switch {
	case spec.Schedule != "":
		panic(fmt.Sprintf("jobs.Define: job %q: jobs.Schedule applies only to jobs.DefineCron", name))
	case spec.DisabledByDefault:
		panic(fmt.Sprintf("jobs.Define: job %q: jobs.DisabledByDefault applies only to jobs.DefineCron", name))
	}

	declareJob[P](name, spec)
	return Def[P]{name: name, spec: spec}
}

func applyDefineOptions(opts []DefineOption) Spec {
	var spec Spec
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

// validateCommon checks what a job and a cron definition share: a snake_case
// name, a label and in-range queue, timeout, attempts and priority.
func validateCommon(fn, name string, spec Spec) {
	switch {
	case !namePattern.MatchString(name):
		panic(fmt.Sprintf("%s: name %q must be snake_case", fn, name))
	case spec.Label == "":
		panic(fmt.Sprintf("%s: %q needs jobs.Label", fn, name))
	case spec.Queue != "" && !slices.Contains(queues, spec.Queue):
		panic(fmt.Sprintf("%s: %q has unknown queue %q", fn, name, spec.Queue))
	case spec.Timeout < 0 || spec.Timeout > maxTimeout:
		panic(fmt.Sprintf("%s: %q Timeout %s must be at most %s", fn, name, spec.Timeout, maxTimeout))
	case spec.Timeout%time.Second != 0:
		panic(fmt.Sprintf("%s: %q Timeout %s must be a whole number of seconds", fn, name, spec.Timeout))
	case spec.MaxAttempts < 0 || spec.MaxAttempts > maxMaxAttempts:
		panic(fmt.Sprintf("%s: %q MaxAttempts %d must be 1-%d", fn, name, spec.MaxAttempts, maxMaxAttempts))
	case spec.Priority != 0 && (spec.Priority < minPriority || spec.Priority > maxPriority):
		panic(fmt.Sprintf("%s: %q Priority %d must be %d-%d", fn, name, spec.Priority, minPriority, maxPriority))
	}
}

// CronDef is a typed cron job definition binding a scheduled job's name,
// schedule and execution defaults.
type CronDef struct {
	name string
	spec Spec
}

// DefineCron declares a cron job named name, called in init() or a
// package-level var. It panics when name is not snake_case, no Label is
// given, Schedule is missing or is not a 5-field cron expression, an option
// is out of range, or an option that applies only to job definitions
// (MaxAttempts, Priority, UniqueBy) is given. An unset Timeout is one hour
// and an unset Queue is QueueBulk.
func DefineCron(name string, opts ...DefineOption) CronDef {
	spec := applyDefineOptions(opts)
	validateCommon("jobs.DefineCron", name, spec)

	switch {
	case spec.MaxAttempts != 0:
		panic(fmt.Sprintf("jobs.DefineCron: cron %q: jobs.MaxAttempts applies only to jobs.Define", name))
	case spec.Priority != 0:
		panic(fmt.Sprintf("jobs.DefineCron: cron %q: jobs.Priority applies only to jobs.Define", name))
	case spec.UniqueBy != "":
		panic(fmt.Sprintf("jobs.DefineCron: cron %q: jobs.UniqueBy applies only to jobs.Define", name))
	case spec.Schedule == "":
		panic(fmt.Sprintf("jobs.DefineCron: cron %q needs jobs.Schedule", name))
	}
	if err := validateSchedule(spec.Schedule); err != nil {
		panic(fmt.Sprintf("jobs.DefineCron: cron %q: %v", name, err))
	}

	if spec.Timeout == 0 {
		spec.Timeout = cronDefaultTimeout
	}
	if spec.Queue == "" {
		spec.Queue = cronDefaultQueue
	}
	declareCron(name, spec)
	return CronDef{name: name, spec: spec}
}

// Name returns the cron job's name.
func (d CronDef) Name() string { return d.name }

// Spec returns the definition's schedule, execution defaults and metadata,
// with the cron defaults applied.
func (d CronDef) Spec() Spec { return d.spec }

// Definition is the payload-type-erased view of a Def, for APIs such as the
// manifest generator that take a definition of any payload type.
type Definition interface {
	Name() string
	Spec() Spec
	PayloadType() reflect.Type
}

// Name returns the job type's name.
func (d Def[P]) Name() string { return d.name }

// Spec returns the definition's execution defaults and metadata.
func (d Def[P]) Spec() Spec { return d.spec }

// PayloadType returns the reflect.Type of the job's payload type P.
func (d Def[P]) PayloadType() reflect.Type { return reflect.TypeFor[P]() }

// Enqueue queues the job, msgpack-encoding payload. Returns the job's ID.
func (d Def[P]) Enqueue(payload P, opts ...JobOption) (string, error) {
	if enqueuer == nil {
		return "", ErrNoEnqueuer
	}
	in, err := d.input(payload, opts)
	if err != nil {
		return "", err
	}
	return enqueuer.Enqueue(abi.JobsEnqueueInput{Type: in.Type, Payload: in.Payload, Opts: in.Opts})
}

// EnqueueTx queues the job inside tx. The job becomes visible to workers
// only if tx commits.
func (d Def[P]) EnqueueTx(tx Tx, payload P, opts ...JobOption) (string, error) {
	if enqueuer == nil {
		return "", ErrNoEnqueuer
	}
	in, err := d.input(payload, opts)
	if err != nil {
		return "", err
	}
	return enqueuer.EnqueueTx(abi.JobsEnqueueTxInput{TxID: tx.TxID(), Type: in.Type, Payload: in.Payload, Opts: in.Opts})
}

// input encodes payload and resolves the enqueue options: each per-enqueue
// option wins over the definition's value, and a value neither sets is left
// zero for the host's manifest and engine defaults.
func (d Def[P]) input(payload P, opts []JobOption) (abi.JobsEnqueueInput, error) {
	o := BuildOptions(opts)
	if o.ProviderModule != "" {
		return abi.JobsEnqueueInput{}, ErrProviderModuleOption
	}

	data, err := msgpack.Marshal(payload)
	if err != nil {
		return abi.JobsEnqueueInput{}, err
	}

	if o.Opts.Queue == "" {
		o.Opts.Queue = d.spec.Queue
	}
	if o.Opts.Priority == 0 {
		o.Opts.Priority = d.spec.Priority
	}
	if o.Opts.MaxAttempts == 0 {
		o.Opts.MaxAttempts = d.spec.MaxAttempts
	}
	return abi.JobsEnqueueInput{Type: d.name, Payload: data, Opts: o.Opts}, nil
}
