// Package sse manages shared event-stream limits, framing, deadlines and shutdown.
package sse

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
)

const KeepAlive = 15 * time.Second

type principal struct{ tenant, user string }

type Manager struct {
	mu      sync.Mutex
	users   map[principal]int
	total   int
	max     int
	perUser int
	age     time.Duration
	ctx     context.Context
	cancel  context.CancelFunc
}

func New(maxConnections, perUser int, age time.Duration) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		users:   make(map[principal]int),
		max:     maxConnections,
		perUser: perUser,
		age:     age,
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancel()
}

type Connection struct {
	ctx        context.Context
	cancel     context.CancelFunc
	stop       func() bool
	manager    *Manager
	principal  principal
	writer     http.ResponseWriter
	controller *http.ResponseController
}

// Open must follow resource authorization and precede any response write.
func (m *Manager) Open(w http.ResponseWriter, r *http.Request, tenant, user string, expires time.Time) (*Connection, bool) {
	key := principal{tenant, user}
	m.mu.Lock()
	if m.ctx.Err() != nil || m.total >= m.max {
		m.mu.Unlock()
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "sse.unavailable", "Event streams are temporarily unavailable")
		return nil, false
	}
	if m.users[key] >= m.perUser {
		m.mu.Unlock()
		httperr.Write(r.Context(), w, http.StatusTooManyRequests, "sse.too_many_connections", "Too many event streams")
		return nil, false
	}
	m.total++
	m.users[key]++
	m.mu.Unlock()

	deadline := time.Now().Add(m.age)
	if !expires.IsZero() && expires.Before(deadline) {
		deadline = expires
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	c := &Connection{
		ctx:        ctx,
		cancel:     cancel,
		manager:    m,
		principal:  key,
		writer:     w,
		controller: http.NewResponseController(w),
	}
	c.stop = context.AfterFunc(m.ctx, cancel)

	// Override the listener's short WriteTimeout for the stream's own lifetime.
	_ = c.controller.SetWriteDeadline(deadline)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	return c, true
}

func (c *Connection) Close() {
	c.cancel()
	c.stop()

	c.manager.mu.Lock()
	defer c.manager.mu.Unlock()
	c.manager.total--
	c.manager.users[c.principal]--
	if c.manager.users[c.principal] == 0 {
		delete(c.manager.users, c.principal)
	}
}

func (c *Connection) write(data string) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if _, err := fmt.Fprint(c.writer, data); err != nil {
		return err
	}
	return c.controller.Flush()
}

// Run sends a snapshot immediately and polls for complete replacement snapshots.
// A nil snapshot means no change. Read failures and cancellation close without done.
func (c *Connection) Run(initial []byte, done bool, interval time.Duration, next func(context.Context) ([]byte, bool, error)) {
	frame := func(data []byte) []byte {
		if len(data) == 0 {
			return nil
		}
		return []byte("data: " + string(data) + "\n\n")
	}
	c.RunFrames(frame(initial), done, interval, func(ctx context.Context) ([]byte, bool, error) {
		data, done, err := next(ctx)
		return frame(data), done, err
	})
}

// RunFrames writes prevalidated SSE frames. Each delay starts after a poll
// completes, and keep-alives continue while that poll is running.
// next must return when its context is cancelled.
func (c *Connection) RunFrames(initial []byte, done bool, interval time.Duration, next func(context.Context) ([]byte, bool, error)) {
	// Cancellation must release a write blocked by a client that stops reading.
	writeDone := make(chan struct{})
	stopWrite := context.AfterFunc(c.ctx, func() {
		_ = c.controller.SetWriteDeadline(time.Now())
		close(writeDone)
	})
	defer func() {
		if !stopWrite() {
			<-writeDone
		}
	}()

	if c.write("retry: 3000\n\n"+string(initial)) != nil {
		return
	}
	if done {
		_ = c.write("event: done\ndata: {}\n\n")
		return
	}

	delay := time.NewTimer(interval)
	defer delay.Stop()
	idle := time.NewTimer(KeepAlive)
	defer idle.Stop()
	type result struct {
		data []byte
		done bool
		err  error
	}
	completed := make(chan result, 1)
	var polls sync.WaitGroup
	pollCtx, cancelPolls := context.WithCancel(c.ctx)
	defer func() {
		cancelPolls()
		polls.Wait()
	}()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-idle.C:
			if c.write(": ping\n\n") != nil {
				return
			}
			idle.Reset(KeepAlive)
		case <-delay.C:
			polls.Go(func() {
				data, done, err := next(pollCtx)
				completed <- result{data, done, err}
			})
		case poll := <-completed:
			if poll.err != nil {
				return
			}
			if len(poll.data) > 0 {
				if c.write(string(poll.data)) != nil {
					return
				}
				idle.Reset(KeepAlive)
			}
			if poll.done {
				_ = c.write("event: done\ndata: {}\n\n")
				return
			}
			delay.Reset(interval)
		}
	}
}
