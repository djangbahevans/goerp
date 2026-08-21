package permcache

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
)

// localRedisConfig mirrors internal/engine/cache's own test convention —
// the compose.dev.yml Redis instance, localhost:6379, no auth.
func localRedisConfig() cache.Config {
	return cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1}
}

func newTestRoleCache(t *testing.T) *RoleCache {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := cache.New(ctx, localRedisConfig())
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	return NewRoleCache(c)
}

func TestRoleCache_GetMissReturnsFoundFalse(t *testing.T) {
	rc := newTestRoleCache(t)

	roleIDs, found := rc.Get(context.Background(), "tenant-"+t.Name(), "user-1")
	if found {
		t.Errorf("Get() found = true for an unset key, want false (roleIDs: %v)", roleIDs)
	}
}

func TestRoleCache_SetThenGetRoundTrips(t *testing.T) {
	rc := newTestRoleCache(t)
	ctx := context.Background()
	tenantID, userID := "tenant-"+t.Name(), "user-1"
	t.Cleanup(func() { _ = rc.Invalidate(context.Background(), tenantID, userID) })

	want := []string{"role-a", "role-b"}
	rc.Set(ctx, tenantID, userID, want)

	got, found := rc.Get(ctx, tenantID, userID)
	if !found {
		t.Fatal("Get() found = false after Set, want true")
	}
	if len(got) != 2 || got[0] != "role-a" || got[1] != "role-b" {
		t.Errorf("Get() = %v, want %v", got, want)
	}
}

func TestRoleCache_Invalidate_RemovesEntry(t *testing.T) {
	rc := newTestRoleCache(t)
	ctx := context.Background()
	tenantID, userID := "tenant-"+t.Name(), "user-1"

	rc.Set(ctx, tenantID, userID, []string{"role-a"})
	if _, found := rc.Get(ctx, tenantID, userID); !found {
		t.Fatal("expected the entry to exist before Invalidate")
	}

	if err := rc.Invalidate(ctx, tenantID, userID); err != nil {
		t.Fatalf("Invalidate() error: %v", err)
	}

	if _, found := rc.Get(ctx, tenantID, userID); found {
		t.Error("Get() found = true after Invalidate, want false")
	}
}

func TestRoleCache_Get_FailsOpenWhenRedisUnavailable(t *testing.T) {
	// cache.New validates connectivity at construction, so there's no way
	// to build a RoleCache around a client that's already unreachable —
	// exercise the fail-open path via a closed client instead, which
	// every subsequent call errors against the same way a genuinely
	// unreachable Redis would.
	c, err := cache.New(context.Background(), localRedisConfig())
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	_ = c.Close()

	rc := NewRoleCache(c)
	if _, found := rc.Get(context.Background(), "t", "u"); found {
		t.Error("Get() against a closed client: found = true, want false (fail open)")
	}
	rc.Set(context.Background(), "t", "u", []string{"role-a"}) // must not panic
}
