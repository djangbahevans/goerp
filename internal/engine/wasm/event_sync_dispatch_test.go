package wasm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// blockingThenFastDispatcher simulates one subscriber whose call blocks
// past its own timeout, and a second that succeeds — proving a
// subscriber_timeout does not stop the remaining ones from running
// (event-system.md §8 "Fan-out and timeout").
type blockingThenFastDispatcher struct {
	slowModule string
}

func (d *blockingThenFastDispatcher) DispatchSync(ctx context.Context, moduleName, handlerName string, payload []byte) (int32, error) {
	if moduleName == d.slowModule {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return 0, nil
}

func TestDispatchSyncSubscribers_AllSucceed(t *testing.T) {
	reg := event.NewEventRegistry()
	reg.Register("sub-a", manifest.Manifest{Subscribes: []manifest.EventSubscription{{Name: "evt", Handler: "h", Async: false}}})

	err := dispatchSyncSubscribers(t.Context(), &fakeSyncEventDispatcher{}, reg, "evt", 1, nil, time.Second)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDispatchSyncSubscribers_SkipsAsyncSubscribers(t *testing.T) {
	reg := event.NewEventRegistry()
	reg.Register("sub-a", manifest.Manifest{Subscribes: []manifest.EventSubscription{{Name: "evt", Handler: "h", Async: true}}})

	dispatcher := &fakeSyncEventDispatcher{}
	if err := dispatchSyncSubscribers(t.Context(), dispatcher, reg, "evt", 1, nil, time.Second); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("expected no dispatch calls for an async subscriber, got %v", dispatcher.calls)
	}
}

func TestDispatchSyncSubscribers_AggregatesAllFailures(t *testing.T) {
	reg := event.NewEventRegistry()
	reg.Register("sub-a", manifest.Manifest{Subscribes: []manifest.EventSubscription{{Name: "evt", Handler: "h1", Async: false}}})
	reg.Register("sub-b", manifest.Manifest{Subscribes: []manifest.EventSubscription{{Name: "evt", Handler: "h2", Async: false}}})

	dispatcher := &fakeSyncEventDispatcher{results: map[string]struct {
		status int32
		err    error
	}{
		"sub-a.h1": {err: errors.New("boom")},
		"sub-b.h2": {status: 2},
	}}

	err := dispatchSyncSubscribers(t.Context(), dispatcher, reg, "evt", 1, nil, time.Second)
	if err == nil {
		t.Fatal("expected an aggregated error")
	}
	if !strings.Contains(err.Error(), "2 of 2") {
		t.Errorf("error = %q, want it to report 2 of 2 failures", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "permanent failure") {
		t.Errorf("error = %q, want it to include both subscribers' failure reasons", err.Error())
	}
}

func TestDispatchSyncSubscribers_TimeoutDoesNotBlockLaterSubscribers(t *testing.T) {
	reg := event.NewEventRegistry()
	reg.Register("slow", manifest.Manifest{Subscribes: []manifest.EventSubscription{{Name: "evt", Handler: "h", Async: false}}})
	reg.Register("fast", manifest.Manifest{Subscribes: []manifest.EventSubscription{{Name: "evt", Handler: "h", Async: false}}})

	err := dispatchSyncSubscribers(t.Context(), &blockingThenFastDispatcher{slowModule: "slow"}, reg, "evt", 1, nil, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error for the timed-out subscriber")
	}
	if !strings.Contains(err.Error(), "subscriber_timeout") {
		t.Errorf("error = %q, want it to mention subscriber_timeout", err.Error())
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("error = %q, want exactly 1 of 2 to have failed (the fast one still succeeded)", err.Error())
	}
}

func TestDispatchSyncSubscribers_OnlyDispatchesToTheEventsVersion(t *testing.T) {
	reg := event.NewEventRegistry()
	reg.Register("sub-v1", manifest.Manifest{Subscribes: []manifest.EventSubscription{{Name: "evt", Handler: "h1", Async: false}}})
	reg.Register("sub-v2", manifest.Manifest{Subscribes: []manifest.EventSubscription{{Name: "evt", Version: 2, Handler: "h2", Async: false}}})

	dispatcher := &fakeSyncEventDispatcher{}
	if err := dispatchSyncSubscribers(t.Context(), dispatcher, reg, "evt", 2, nil, time.Second); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(dispatcher.calls) != 1 || dispatcher.calls[0] != "sub-v2.h2" {
		t.Fatalf("calls = %v, want only sub-v2.h2 for a v2 event", dispatcher.calls)
	}
}
