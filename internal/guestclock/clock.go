// Package guestclock configures WASI clocks, cancellable sleep and secure entropy.
package guestclock

import (
	"context"
	"crypto/rand"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/sys"
)

type invocationTimeKey struct{}

// WithTime carries a fixed wall-clock time through an invocation's nested calls.
func WithTime(ctx context.Context, acceptedAt time.Time) context.Context {
	return context.WithValue(ctx, invocationTimeKey{}, acceptedAt.UTC())
}

// InitializationContext preserves cancellation while removing any caller's fixed time.
func InitializationContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, invocationTimeKey{}, nil)
}

// Now returns the invocation's fixed time, or the source's current time when unbound.
func Now(ctx context.Context, source func() time.Time) time.Time {
	if acceptedAt, ok := ctx.Value(invocationTimeKey{}).(time.Time); ok {
		return acceptedAt
	}

	return source().UTC()
}

// Clock belongs to one instance. Bind and guest execution require exclusive ownership.
type Clock struct {
	source     func() time.Time
	ctx        context.Context
	acceptedAt time.Time
	bound      bool
}

func New(ctx context.Context, source func() time.Time) *Clock {
	return &Clock{source: source, ctx: ctx}
}

// Bind fixes wall time and sleep cancellation for this invocation. Nested calls inherit
// the accepted time while retaining their own context deadlines.
func (c *Clock) Bind(ctx context.Context) context.Context {
	acceptedAt := Now(ctx, c.source)
	ctx = WithTime(ctx, acceptedAt)
	c.ctx = ctx
	c.acceptedAt = acceptedAt
	c.bound = true

	return ctx
}

func (c *Clock) Configure(base wazero.ModuleConfig) wazero.ModuleConfig {
	return base.
		WithWalltime(c.walltime, sys.ClockResolution(time.Microsecond)).
		WithSysNanotime().
		WithNanosleep(c.nanosleep).
		WithRandSource(rand.Reader)
}

func (c *Clock) walltime() (int64, int32) {
	now := c.acceptedAt
	if !c.bound {
		now = c.source().UTC()
	}

	return now.Unix(), int32(now.Nanosecond())
}

func (c *Clock) nanosleep(ns int64) {
	if ns <= 0 || c.ctx.Err() != nil {
		return
	}

	timer := time.NewTimer(time.Duration(ns))
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-c.ctx.Done():
	}
}
