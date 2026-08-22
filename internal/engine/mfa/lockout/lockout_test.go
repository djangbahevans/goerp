package lockout

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
)

func openTestCounter(t *testing.T) (*Counter, string, string) {
	t.Helper()
	ctx := context.Background()

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	userID := fmt.Sprintf("user-%d", time.Now().UnixNano())
	tenantID := "tenant-1"
	t.Cleanup(func() { _ = cacheClient.Delete(context.Background(), key(userID, tenantID)) })

	return NewCounter(cacheClient), userID, tenantID
}

func TestLocked_FalseWithNoFailures(t *testing.T) {
	c, userID, tenantID := openTestCounter(t)

	locked, err := c.Locked(context.Background(), userID, tenantID)
	if err != nil {
		t.Fatalf("Locked() error: %v", err)
	}
	if locked {
		t.Error("Locked() = true with no recorded failures, want false")
	}
}

func TestRecordFailure_LocksAfterMaxAttempts(t *testing.T) {
	c, userID, tenantID := openTestCounter(t)
	ctx := context.Background()

	for i := range MaxAttempts - 1 {
		if err := c.RecordFailure(ctx, userID, tenantID); err != nil {
			t.Fatalf("RecordFailure() error on attempt %d: %v", i+1, err)
		}
		locked, err := c.Locked(ctx, userID, tenantID)
		if err != nil {
			t.Fatalf("Locked() error: %v", err)
		}
		if locked {
			t.Fatalf("Locked() = true after %d failures, want false (threshold is %d)", i+1, MaxAttempts)
		}
	}

	if err := c.RecordFailure(ctx, userID, tenantID); err != nil {
		t.Fatalf("RecordFailure() error on final attempt: %v", err)
	}
	locked, err := c.Locked(ctx, userID, tenantID)
	if err != nil {
		t.Fatalf("Locked() error: %v", err)
	}
	if !locked {
		t.Errorf("Locked() = false after %d failures, want true", MaxAttempts)
	}
}

func TestReset_ClearsLockout(t *testing.T) {
	c, userID, tenantID := openTestCounter(t)
	ctx := context.Background()

	for range MaxAttempts {
		if err := c.RecordFailure(ctx, userID, tenantID); err != nil {
			t.Fatalf("RecordFailure() error: %v", err)
		}
	}
	if locked, err := c.Locked(ctx, userID, tenantID); err != nil || !locked {
		t.Fatalf("expected locked before Reset(), locked=%v err=%v", locked, err)
	}

	if err := c.Reset(ctx, userID, tenantID); err != nil {
		t.Fatalf("Reset() error: %v", err)
	}

	locked, err := c.Locked(ctx, userID, tenantID)
	if err != nil {
		t.Fatalf("Locked() error: %v", err)
	}
	if locked {
		t.Error("Locked() = true after Reset(), want false")
	}
}

func TestLocked_ScopedPerUserTenantPair(t *testing.T) {
	c, userID, tenantID := openTestCounter(t)
	ctx := context.Background()
	otherTenantID := tenantID + "-other"
	t.Cleanup(func() { _ = c.Reset(ctx, userID, otherTenantID) })

	for range MaxAttempts {
		if err := c.RecordFailure(ctx, userID, tenantID); err != nil {
			t.Fatalf("RecordFailure() error: %v", err)
		}
	}

	locked, err := c.Locked(ctx, userID, otherTenantID)
	if err != nil {
		t.Fatalf("Locked() error: %v", err)
	}
	if locked {
		t.Error("Locked() = true for a different tenant, want false — counter must be scoped per (user_id, tenant_id)")
	}
}
