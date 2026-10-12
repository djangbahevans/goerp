package wasm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestInstancePoolConcurrentDrainAndClose(t *testing.T) {
	pool := newTestPoolWithReplenish(t, emptyModule, PoolConfig{
		WarmSize: 2,
		MaxSize:  2,
	})
	waitFor(t, time.Second, func() bool { return pool.IdleCount() == 2 })

	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if err := pool.DrainAndClose(t.Context(), time.Second); err != nil {
				t.Errorf("drain pool: %v", err)
			}
		})
	}
	wg.Wait()
	if err := pool.DrainAndClose(t.Context(), time.Second); err != nil {
		t.Fatal(err)
	}

	if got := pool.closed.Load(); got != 2 {
		t.Fatalf("closed %d instances, want 2", got)
	}
	if got := len(pool.tokens); got != 0 {
		t.Fatalf("retained %d tokens after drain", got)
	}
	if _, err := pool.Borrow(t.Context()); !errors.Is(err, ErrPoolDraining) {
		t.Fatalf("borrow after concurrent drain: %v", err)
	}
}

func TestInstancePoolDrainAndCloseReportsTimeout(t *testing.T) {
	pool := newTestPoolWithReplenish(t, emptyModule, PoolConfig{
		WarmSize: 1,
		MaxSize:  1,
	})
	inst, err := pool.Borrow(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if err := pool.DrainAndClose(t.Context(), 20*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain with a borrowed instance: %v, want deadline exceeded", err)
	}
	pool.Return(inst)
	if err := pool.DrainAndClose(t.Context(), time.Second); err != nil {
		t.Fatalf("drain after return: %v", err)
	}
}
