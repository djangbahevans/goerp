package wasm

import (
	"context"
	"time"

	"github.com/djangbahevans/goerp/internal/guestclock"
)

// WithInvocationTime fixes wall-clock reads across an invocation and its nested calls.
func WithInvocationTime(ctx context.Context, acceptedAt time.Time) context.Context {
	return guestclock.WithTime(ctx, acceptedAt)
}

// WithClock supplies the authoritative wall clock. It must be safe for concurrent calls.
// Monotonic measurements and sleep durations remain real elapsed time.
func WithClock(now func() time.Time) Option {
	return func(r *Runtime) { r.clock = now }
}

// Option configures the runtime before modules are instantiated.
type Option func(*Runtime)

// Now reads the authoritative wall clock in UTC.
func (r *Runtime) Now() time.Time {
	if r == nil || r.clock == nil {
		return time.Now().UTC()
	}

	return r.clock().UTC()
}

func (r *Runtime) invocationTime(ctx context.Context) time.Time {
	return guestclock.Now(ctx, r.Now)
}

func (inst *ModuleInstance) invocationContext(ctx context.Context) context.Context {
	return inst.clock.Bind(ctx)
}
