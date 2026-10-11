package modeltest

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/eventdelivery"
	"github.com/djangbahevans/goerp/internal/engine/jobdispatch"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

// EventOption configures a synchronous harness delivery.
type EventOption func(*eventOptions)

type eventOptions struct{ id string }

// WithEventID reuses id and its emission timestamp for duplicate-delivery tests.
func WithEventID(id string) EventOption {
	return func(o *eventOptions) { o.id = id }
}

// Deliver invokes the module's subscriber for def synchronously through WASM.
// Transactional subscribers commit their writes and delivery claim together.
// Errors preserve retryable versus permanent status, rather than the guest's error text.
func (h *Harness) Deliver[P any](def events.Def[P], payload P, opts ...EventOption) error {
	h.t.Helper()
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode event payload: %w", err)
	}

	return h.deliver(def.Name(), def.Version(), data, opts)
}

// DeliverRaw sends bytes through the subscriber's payload decoder.
// An undecodable payload returns a permanent delivery error without calling the handler.
func (h *Harness) DeliverRaw[P any](def events.Def[P], data []byte) error {
	h.t.Helper()
	return h.deliver(def.Name(), def.Version(), data, nil)
}

func (h *Harness) deliver(name string, version int, data []byte, opts []EventOption) error {
	o := eventOptions{id: uuid.New().String()}
	for _, opt := range opts {
		opt(&o)
	}
	if o.id == "" {
		return errors.New("modeltest: event ID must not be empty")
	}

	now := h.runtime.Now()
	// The production ledger identifies a delivery by ID and original emission time.
	emittedAt, _ := h.eventTimes.LoadOrStore(o.id, now)
	env := event.Envelope{
		ID:            o.id,
		Name:          name,
		Version:       version,
		EmitterModule: h.moduleName,
		TenantID:      h.TenantID,
		UserID:        h.UserID,
		TraceID:       uuid.New().String(),
		EmittedAt:     emittedAt.(time.Time),
		Payload:       data,
	}
	invoker := &eventdelivery.HandlerInvoker{Runtime: h.runtime, TenantStore: h.tenants, Roles: h.roles}
	status, err := invoker.Deliver(wasm.WithInvocationTime(h.t.Context(), now), h.registry.Snapshot(), h.moduleName, env)
	if err != nil {
		return fmt.Errorf("modeltest: deliver %s v%d: %w", name, version, err)
	}
	if status == 0 {
		return nil
	}
	err = fmt.Errorf("modeltest: subscriber for %s v%d returned status %d", name, version, status)
	if status == 2 {
		return events.PermanentError(err)
	}
	return err
}

// JobOption configures synchronous harness execution.
type JobOption func(*jobOptions)

type jobOptions struct{ attempt int }

// WithJobAttempt overrides the default attempt of 1. Attempts must be positive.
func WithJobAttempt(n int) JobOption {
	return func(o *jobOptions) { o.attempt = n }
}

// RunJob invokes the module's registered handler for def synchronously through WASM.
// It uses the harness's tenant and default user, resolving current tenant membership.
// Permanent failures return jobs.PermanentError; guest error text is not carried by the ABI.
func (h *Harness) RunJob[P any](def jobs.Def[P], payload P, opts ...JobOption) error {
	h.t.Helper()
	o := jobOptions{attempt: 1}
	for _, opt := range opts {
		opt(&o)
	}
	if o.attempt < 1 {
		return errors.New("modeltest: job attempt must be positive")
	}
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode job payload: %w", err)
	}

	maxAttempts := 3
	for _, entry := range h.module.Manifest.JobTypes {
		if entry.Name == def.Name() {
			maxAttempts = cmp.Or(entry.MaxAttempts, 3)
			break
		}
	}
	worker := &jobdispatch.Worker{
		ModuleRegistry: h.registry,
		Runtime:        h.runtime,
		TenantStore:    h.tenants,
		Roles:          h.roles,
	}
	job := &river.Job[jobqueue.WASMJobArgs]{
		JobRow: &rivertype.JobRow{Attempt: o.attempt, MaxAttempts: maxAttempts},
		Args: jobqueue.WASMJobArgs{
			ModuleName: h.moduleName,
			JobType:    def.Name(),
			TenantID:   h.TenantID,
			UserID:     h.UserID,
			TraceID:    uuid.New().String(),
			Payload:    data,
		},
	}
	ctx := wasm.WithInvocationTime(h.t.Context(), h.runtime.Now())
	if timeout := worker.Timeout(job); timeout > 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	err = worker.Work(ctx, job)
	if _, ok := errors.AsType[*river.JobCancelError](err); ok {
		return jobs.PermanentError(err)
	}
	return err
}
