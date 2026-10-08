package guestclock

import (
	"context"
	"testing"
	"time"
)

func TestSleepCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	clock := New(ctx, time.Now)
	done := make(chan struct{})
	go func() {
		clock.nanosleep(int64(time.Hour))
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sleep outlived cancellation")
	}
}

func TestBoundTimeAndInitialization(t *testing.T) {
	sourceTime := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := New(t.Context(), func() time.Time { return sourceTime })
	if sec, _ := clock.walltime(); sec != sourceTime.Unix() {
		t.Errorf("initialization clock = %d, want %d", sec, sourceTime.Unix())
	}

	acceptedAt := sourceTime.Add(time.Hour)
	ctx := clock.Bind(WithTime(t.Context(), acceptedAt))
	sourceTime = sourceTime.Add(2 * time.Hour)
	if sec, _ := clock.walltime(); sec != acceptedAt.Unix() {
		t.Errorf("bound clock = %d, want %d", sec, acceptedAt.Unix())
	}

	child := New(ctx, func() time.Time { return sourceTime })
	child.Bind(ctx)
	if sec, _ := child.walltime(); sec != acceptedAt.Unix() {
		t.Errorf("nested clock = %d, want %d", sec, acceptedAt.Unix())
	}

	if got := Now(InitializationContext(ctx), func() time.Time { return sourceTime }); !got.Equal(sourceTime) {
		t.Errorf("initialization time = %v, want %v", got, sourceTime)
	}

	clock.Bind(WithTime(t.Context(), time.Time{}))
	if sec, _ := clock.walltime(); sec != (time.Time{}).Unix() {
		t.Errorf("zero accepted time = %d, want year one", sec)
	}
}
