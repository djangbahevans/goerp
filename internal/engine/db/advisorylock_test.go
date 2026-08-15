package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// openTestPool uses localPostgresDSN — the PgBouncer-fronted address
// (transaction pooling, compose.dev.yml POOL_MODE: transaction) — not a
// direct-to-Postgres one, deliberately: WithAdvisoryLock exists precisely
// because a session-level lock isn't safe over this kind of connection,
// so its own test needs to run against exactly the connection type the
// bug report (goerp#171) is about.
func openTestPool(t *testing.T) *sql.DB {
	t.Helper()

	pool, err := New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	return pool
}

func TestWithAdvisoryLock_SerializesConcurrentHoldersOfSameKey(t *testing.T) {
	pool := openTestPool(t)
	key := AdvisoryLockKey(fmt.Sprintf("test-serialize-%d", time.Now().UnixNano()))

	var mu sync.Mutex
	var inFlight, maxInFlight int
	hold := func() error {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			errs <- WithAdvisoryLock(context.Background(), pool, []int64{key}, func(tx *sql.Tx) error {
				return hold()
			})
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("WithAdvisoryLock() error: %v", err)
		}
	}
	if maxInFlight != 1 {
		t.Errorf("max concurrent holders of the same key = %d, want 1 (they should have serialized)", maxInFlight)
	}
}

func TestWithAdvisoryLock_DifferentKeysDoNotSerialize(t *testing.T) {
	pool := openTestPool(t)
	suffix := time.Now().UnixNano()
	keyA := AdvisoryLockKey(fmt.Sprintf("test-parallel-a-%d", suffix))
	keyB := AdvisoryLockKey(fmt.Sprintf("test-parallel-b-%d", suffix))

	var mu sync.Mutex
	var inFlight, maxInFlight int
	hold := func() error {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		time.Sleep(100 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, key := range []int64{keyA, keyB} {
		wg.Go(func() {
			errs <- WithAdvisoryLock(context.Background(), pool, []int64{key}, func(tx *sql.Tx) error {
				return hold()
			})
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("WithAdvisoryLock() error: %v", err)
		}
	}
	if maxInFlight != 2 {
		t.Errorf("max concurrent holders of different keys = %d, want 2 (they should not have serialized against each other)", maxInFlight)
	}
}

func TestWithAdvisoryLock_FnErrorRollsBackDDL(t *testing.T) {
	pool := openTestPool(t)
	key := AdvisoryLockKey(fmt.Sprintf("test-rollback-%d", time.Now().UnixNano()))
	table := fmt.Sprintf("advisory_lock_rollback_test_%d", time.Now().UnixNano())

	sentinel := errors.New("boom")
	err := WithAdvisoryLock(context.Background(), pool, []int64{key}, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(context.Background(), "CREATE TABLE "+table+" (id INT)"); err != nil {
			t.Fatalf("create fixture table inside tx: %v", err)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithAdvisoryLock() error = %v, want %v", err, sentinel)
	}

	var exists bool
	if scanErr := pool.QueryRowContext(context.Background(),
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)", table,
	).Scan(&exists); scanErr != nil {
		t.Fatalf("check table existence: %v", scanErr)
	}
	if exists {
		t.Error("expected the table created inside fn to be rolled back after fn returned an error")
	}
}

func TestWithAdvisoryLock_MultipleKeysBothAcquired(t *testing.T) {
	pool := openTestPool(t)
	suffix := time.Now().UnixNano()
	keyA := AdvisoryLockKey(fmt.Sprintf("test-multi-a-%d", suffix))
	keyB := AdvisoryLockKey(fmt.Sprintf("test-multi-b-%d", suffix))

	called := false
	err := WithAdvisoryLock(context.Background(), pool, []int64{keyB, keyA}, func(tx *sql.Tx) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("WithAdvisoryLock() error: %v", err)
	}
	if !called {
		t.Error("expected fn to run once both keys were acquired")
	}
}
