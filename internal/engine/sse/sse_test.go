package sse

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLimitsAndRelease(t *testing.T) {
	m := New(2, 1, time.Minute)
	defer m.Shutdown()
	request := httptest.NewRequest("GET", "/", nil)
	a, ok := m.Open(httptest.NewRecorder(), request, "tenant", "user", time.Time{})
	if !ok {
		t.Fatal("first connection refused")
	}
	rec := httptest.NewRecorder()
	if _, ok := m.Open(rec, request, "tenant", "user", time.Time{}); ok || rec.Code != 429 || !strings.Contains(rec.Body.String(), "sse.too_many_connections") {
		t.Fatalf("per user limit: %d %s", rec.Code, rec.Body)
	}
	b, ok := m.Open(httptest.NewRecorder(), request, "other-tenant", "user", time.Time{})
	if !ok {
		t.Fatal("same user in another tenant shares quota")
	}
	rec = httptest.NewRecorder()
	if _, ok := m.Open(rec, request, "tenant", "other-user", time.Time{}); ok || rec.Code != 503 || !strings.Contains(rec.Body.String(), "sse.unavailable") {
		t.Fatalf("instance limit: %d %s", rec.Code, rec.Body)
	}
	a.Close()
	c, ok := m.Open(httptest.NewRecorder(), request, "tenant", "user", time.Time{})
	if !ok {
		t.Fatal("quota not released")
	}
	b.Close()
	c.Close()
}

func TestSnapshotDoneAndHeaders(t *testing.T) {
	m := New(1, 1, time.Minute)
	defer m.Shutdown()
	rec := httptest.NewRecorder()
	c, ok := m.Open(rec, httptest.NewRequest("GET", "/", nil), "tenant", "user", time.Time{})
	if !ok {
		t.Fatal("open")
	}
	defer c.Close()
	c.Run([]byte(`{"status":"complete"}`), true, time.Second, nil)
	if rec.Header().Get("Content-Type") != "text/event-stream" || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers = %v", rec.Header())
	}
	if rec.Body.String() != "retry: 3000\n\ndata: {\"status\":\"complete\"}\n\nevent: done\ndata: {}\n\n" {
		t.Fatal(rec.Body.String())
	}
}

func TestLifetimeAndShutdown(t *testing.T) {
	for _, mode := range []string{"age", "token", "shutdown", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			age, expiry := time.Minute, time.Time{}
			if mode == "age" {
				age = 50 * time.Millisecond
			}
			if mode == "token" {
				expiry = time.Now().Add(50 * time.Millisecond)
			}
			m := New(1, 1, age)
			defer m.Shutdown()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c, ok := m.Open(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, "GET", "/", nil), "tenant", "user", expiry)
			if !ok {
				t.Fatal("open")
			}
			defer c.Close()
			finished := make(chan struct{})
			go func() { c.Run([]byte(`{}`), false, time.Hour, nil); close(finished) }()
			if mode == "shutdown" {
				m.Shutdown()
			}
			if mode == "disconnect" {
				cancel()
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("stream did not close")
			}
		})
	}
}

func TestKeepAliveWithRealHTTPWriteTimeout(t *testing.T) {
	m := New(1, 1, 20*time.Second)
	defer m.Shutdown()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := m.Open(w, r, "tenant", "user", time.Time{})
		if !ok {
			return
		}
		defer c.Close()
		c.Run([]byte(`{}`), false, time.Second, func(context.Context) ([]byte, bool, error) { return nil, false, nil })
	}))
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL, nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	start := time.Now()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream closed before ping: %v", err)
		}
		if line == ": ping\n" {
			break
		}
	}
	if time.Since(start) < 14*time.Second {
		t.Fatal("ping sent before idle period")
	}
	m.Shutdown()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

// blockedWriter models a client whose transport stops accepting response bytes.
type blockedWriter struct {
	entered  chan struct{}
	released chan struct{}
	once     sync.Once
}

func (w *blockedWriter) Header() http.Header { return make(http.Header) }
func (w *blockedWriter) WriteHeader(int)     {}
func (w *blockedWriter) Flush()              {}
func (w *blockedWriter) Write([]byte) (int, error) {
	close(w.entered)
	<-w.released
	return 0, context.Canceled
}
func (w *blockedWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.After(time.Now()) {
		w.once.Do(func() { close(w.released) })
	}
	return nil
}

func TestShutdownReleasesBlockedWrite(t *testing.T) {
	m := New(1, 1, time.Hour)
	defer m.Shutdown()
	writer := &blockedWriter{entered: make(chan struct{}), released: make(chan struct{})}
	connection, ok := m.Open(writer, httptest.NewRequest("GET", "/", nil), "tenant", "user", time.Time{})
	if !ok {
		t.Fatal("open")
	}
	defer connection.Close()
	done := make(chan struct{})
	go func() { connection.Run([]byte(`{}`), false, time.Hour, nil); close(done) }()
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	m.Shutdown()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked behind response write")
	}
}

func TestFramePollWaitStartsAfterCompletion(t *testing.T) {
	m := New(1, 1, 2*time.Second)
	defer m.Shutdown()
	rec := httptest.NewRecorder()
	c, ok := m.Open(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/", nil), "tenant", "user", time.Time{})
	if !ok {
		t.Fatal("open")
	}
	defer c.Close()
	var starts []time.Time
	c.RunFrames(nil, false, 30*time.Millisecond, func(ctx context.Context) ([]byte, bool, error) {
		starts = append(starts, time.Now())
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		return []byte("id: next\ndata: {}\n\n"), len(starts) == 3, nil
	})
	if len(starts) != 3 {
		t.Fatalf("polls=%d", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 80*time.Millisecond {
			t.Fatalf("poll began before full interval: %s", starts[i].Sub(starts[i-1]))
		}
	}
}

func TestFramesKeepAliveDuringPollAndDisconnect(t *testing.T) {
	m := New(1, 1, 20*time.Second)
	defer m.Shutdown()
	finished := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		c, ok := m.Open(w, r, "tenant", "user", time.Time{})
		if !ok {
			return
		}
		defer c.Close()
		c.RunFrames(nil, false, 10*time.Millisecond, func(ctx context.Context) ([]byte, bool, error) {
			<-ctx.Done()
			return nil, false, ctx.Err()
		})
	}))
	server.Config.WriteTimeout = 100 * time.Millisecond
	server.Start()
	defer server.Close()
	server.Client().Timeout = 18 * time.Second
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == ": ping\n" {
			break
		}
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel the in-flight poll")
	}
}
