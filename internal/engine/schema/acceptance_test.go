package schema

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAcceptedHashes_ScopedToModuleVersion(t *testing.T) {
	_, pool := openTestPool(t, 5*time.Second)
	ctx := context.Background()
	tenantID := uuid.NewString()

	if _, err := pool.RecordAcceptance(ctx, tenantID, "sales", "1.0.0", "hash-a", "reviewed", "operator-1"); err != nil {
		t.Fatalf("RecordAcceptance() error: %v", err)
	}

	sameVersion, err := pool.AcceptedHashes(ctx, tenantID, "sales", "1.0.0")
	if err != nil {
		t.Fatalf("AcceptedHashes(1.0.0) error: %v", err)
	}
	if !sameVersion["hash-a"] {
		t.Errorf("AcceptedHashes(1.0.0) = %v, want hash-a present", sameVersion)
	}

	// The whole point: an acceptance recorded against 1.0.0 must never
	// match when checking against a different (e.g. since-upgraded)
	// version, even though the target_hash is identical — a structurally
	// identical change in a different version's diff was never reviewed
	// under this acceptance.
	otherVersion, err := pool.AcceptedHashes(ctx, tenantID, "sales", "1.1.0")
	if err != nil {
		t.Fatalf("AcceptedHashes(1.1.0) error: %v", err)
	}
	if otherVersion["hash-a"] {
		t.Errorf("AcceptedHashes(1.1.0) = %v, want hash-a NOT present (recorded under a different version)", otherVersion)
	}
}

func TestRecordAcceptance_ConcurrentDuplicateCallsConvergeOnOneRow(t *testing.T) {
	conn, pool := openTestPool(t, 5*time.Second)
	ctx := context.Background()
	tenantID := uuid.NewString()

	const n = 5
	ids := make(chan string, n)
	errs := make(chan error, n)
	for range n {
		go func() {
			id, err := pool.RecordAcceptance(ctx, tenantID, "sales", "1.0.0", "hash-b", "reviewed", "operator-1")
			ids <- id
			errs <- err
		}()
	}

	firstID := ""
	for range n {
		if err := <-errs; err != nil {
			t.Fatalf("RecordAcceptance() error: %v", err)
		}
		id := <-ids
		if firstID == "" {
			firstID = id
		} else if id != firstID {
			t.Errorf("RecordAcceptance() returned id %q, want every concurrent call to converge on %q", id, firstID)
		}
	}

	var count int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM system.schema_sync_acceptances WHERE tenant_id = $1 AND module_name = 'sales' AND target_hash = 'hash-b'",
		tenantID,
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want exactly 1 despite %d concurrent RecordAcceptance calls", count, n)
	}
}
