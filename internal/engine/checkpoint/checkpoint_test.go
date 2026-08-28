package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}
	return store
}

func uniqueJobID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("checkpointtest%d", time.Now().UnixNano())
}

func cleanupCheckpoint(t *testing.T, store *Store, jobID, module string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(),
			"DELETE FROM system.job_checkpoints WHERE job_id = $1 AND module = $2", jobID, module)
	})
}

func TestAcquireLease_CreatesRowAndSucceedsForNewCheckpoint(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	jobID := uniqueJobID(t)
	cleanupCheckpoint(t, store, jobID, "contacts")

	progress, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-1", time.Minute)
	if err != nil {
		t.Fatalf("AcquireLease() error: %v", err)
	}
	if progress.LastID != "" {
		t.Errorf("LastID = %q, want empty for a brand-new checkpoint", progress.LastID)
	}
}

func TestAcquireLease_FailsFastAgainstLiveConcurrentLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	jobID := uniqueJobID(t)
	cleanupCheckpoint(t, store, jobID, "contacts")

	if _, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-1", time.Minute); err != nil {
		t.Fatalf("first AcquireLease() error: %v", err)
	}

	_, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-2", time.Minute)
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second AcquireLease() error = %v, want ErrLeaseHeld", err)
	}
}

func TestAcquireLease_ReclaimsStaleLease(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	jobID := uniqueJobID(t)
	cleanupCheckpoint(t, store, jobID, "contacts")

	if _, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-1", time.Minute); err != nil {
		t.Fatalf("first AcquireLease() error: %v", err)
	}

	// A negative staleAfter makes every existing heartbeat look stale —
	// simulating "run-1 crashed a while ago" without needing to sleep.
	progress, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-2", -time.Minute)
	if err != nil {
		t.Fatalf("reclaiming AcquireLease() error: %v", err)
	}
	if progress == nil {
		t.Fatal("expected a non-nil Progress from a successful reclaim")
	}
}

func TestAdvanceCheckpoint_PersistsAcrossASimulatedCrash(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	jobID := uniqueJobID(t)
	cleanupCheckpoint(t, store, jobID, "contacts")

	progress, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-1", time.Minute)
	if err != nil {
		t.Fatalf("AcquireLease() error: %v", err)
	}
	if err := progress.AdvanceCheckpoint(ctx, "01H000"); err != nil {
		t.Fatalf("AdvanceCheckpoint() error: %v", err)
	}

	// Simulate a crash: no MarkComplete/MarkFailed call. A fresh
	// AcquireLease (reclaiming, since run-1's heartbeat is now "stale"
	// per a negative staleAfter) must see the last-advanced LastID, not
	// empty.
	resumed, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-2", -time.Minute)
	if err != nil {
		t.Fatalf("resuming AcquireLease() error: %v", err)
	}
	if resumed.LastID != "01H000" {
		t.Errorf("resumed.LastID = %q, want %q", resumed.LastID, "01H000")
	}
}

func TestMarkComplete_IsTerminal(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	jobID := uniqueJobID(t)
	cleanupCheckpoint(t, store, jobID, "contacts")

	progress, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-1", time.Minute)
	if err != nil {
		t.Fatalf("AcquireLease() error: %v", err)
	}
	if err := progress.MarkComplete(ctx); err != nil {
		t.Fatalf("MarkComplete() error: %v", err)
	}

	_, err = store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-2", time.Minute)
	if !errors.Is(err, ErrAlreadyComplete) {
		t.Fatalf("AcquireLease() after MarkComplete error = %v, want ErrAlreadyComplete", err)
	}
}

func TestMarkFailed_AllowsRetryFromLastCheckpoint(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	jobID := uniqueJobID(t)
	cleanupCheckpoint(t, store, jobID, "contacts")

	progress, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-1", time.Minute)
	if err != nil {
		t.Fatalf("AcquireLease() error: %v", err)
	}
	if err := progress.AdvanceCheckpoint(ctx, "01H000"); err != nil {
		t.Fatalf("AdvanceCheckpoint() error: %v", err)
	}
	if err := progress.MarkFailed(ctx); err != nil {
		t.Fatalf("MarkFailed() error: %v", err)
	}

	retry, err := store.AcquireLease(ctx, jobID, "contacts", "11111111-1111-1111-1111-111111111111", "run-2", time.Minute)
	if err != nil {
		t.Fatalf("retry AcquireLease() error: %v", err)
	}
	if retry.LastID != "01H000" {
		t.Errorf("retry.LastID = %q, want %q", retry.LastID, "01H000")
	}
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store := openTestStore(t)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() error: %v", err)
	}
}
