package cache

import (
	"context"
	"testing"
	"time"
)

// localRedisConfig points at the compose.dev.yml Redis instance
// (localhost:6379, no auth). Tests using it are skipped if it isn't
// reachable, so `go test ./...` still passes without Docker running.
func localRedisConfig() Config {
	return Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1}
}

func skipIfUnreachable(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
}

func TestNewConnectsAndPings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)

	if err := c.Ping(ctx); err != nil {
		t.Errorf("Ping() error: %v", err)
	}
}

func TestNewInvalidAddr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := New(ctx, Config{Addr: "127.0.0.1:1", MaxRetries: 0})
	if err == nil {
		t.Fatal("New() with an unreachable address: expected an error, got nil")
	}
}

func TestSetWithTTLAndExists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	// t.Cleanup, not defer, for both of these: a deferred Close would run
	// before t.Cleanup fires, closing c before the key-delete cleanup
	// below gets a chance to use it. Registered in this order so the
	// delete (registered second, LIFO) runs before the close (first).
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })

	exists, err := c.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists() before Set error: %v", err)
	}
	if exists {
		t.Fatal("Exists() = true before Set, want false")
	}

	if err := c.SetWithTTL(ctx, key, "1", time.Minute); err != nil {
		t.Fatalf("SetWithTTL() error: %v", err)
	}

	exists, err = c.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists() after Set error: %v", err)
	}
	if !exists {
		t.Error("Exists() = false after SetWithTTL, want true")
	}
}

func TestSetWithTTL_ExpiresKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })
	if err := c.SetWithTTL(ctx, key, "1", 50*time.Millisecond); err != nil {
		t.Fatalf("SetWithTTL() error: %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	exists, err := c.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists() error: %v", err)
	}
	if exists {
		t.Error("Exists() = true after TTL elapsed, want false")
	}
}

func TestSetNXWithTTL_SetsUnsetKeyAndReportsTrue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })

	set, err := c.SetNXWithTTL(ctx, key, "1", time.Minute)
	if err != nil {
		t.Fatalf("SetNXWithTTL() error: %v", err)
	}
	if !set {
		t.Error("SetNXWithTTL() set = false for a previously unset key, want true")
	}

	exists, err := c.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists() error: %v", err)
	}
	if !exists {
		t.Error("Exists() = false after SetNXWithTTL, want true")
	}
}

func TestSetNXWithTTL_AlreadySetReportsFalseAndDoesNotOverwrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })

	if err := c.SetWithTTL(ctx, key, "first", time.Minute); err != nil {
		t.Fatalf("SetWithTTL() error: %v", err)
	}

	set, err := c.SetNXWithTTL(ctx, key, "second", time.Minute)
	if err != nil {
		t.Fatalf("SetNXWithTTL() error: %v", err)
	}
	if set {
		t.Error("SetNXWithTTL() set = true for an already-set key, want false")
	}

	value, _, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if value != "first" {
		t.Errorf("value = %q after a failed SetNXWithTTL, want unchanged %q", value, "first")
	}
}

func TestGet_MissReturnsFoundFalse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	defer func() { _ = c.Close() }()

	key := "cache-test:" + t.Name()

	value, found, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if found {
		t.Errorf("Get() found = true for an unset key, want false")
	}
	if value != "" {
		t.Errorf("Get() value = %q for an unset key, want empty", value)
	}
}

func TestGet_ReadsBackSetValue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })
	if err := c.SetWithTTL(ctx, key, "some-value", time.Minute); err != nil {
		t.Fatalf("SetWithTTL() error: %v", err)
	}

	value, found, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !found {
		t.Fatal("Get() found = false after SetWithTTL, want true")
	}
	if value != "some-value" {
		t.Errorf("Get() value = %q, want %q", value, "some-value")
	}
}

func TestDelete_RemovesKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	defer func() { _ = c.Close() }()

	key := "cache-test:" + t.Name()
	if err := c.SetWithTTL(ctx, key, "1", time.Minute); err != nil {
		t.Fatalf("SetWithTTL() error: %v", err)
	}

	if err := c.Delete(ctx, key); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	exists, err := c.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists() error: %v", err)
	}
	if exists {
		t.Error("Exists() = true after Delete, want false")
	}
}

func TestDelete_UnsetKeyIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	defer func() { _ = c.Close() }()

	if err := c.Delete(ctx, "cache-test:"+t.Name()); err != nil {
		t.Errorf("Delete() on an unset key: error = %v, want nil", err)
	}
}

func TestDeleteByPrefix_RemovesOnlyMatchingKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	prefix := "cache-test:" + t.Name() + ":"
	matching := []string{prefix + "a", prefix + "b", prefix + "c"}
	other := "cache-test:" + t.Name() + "-unrelated"
	t.Cleanup(func() { _ = c.Delete(context.Background(), other) })

	for _, key := range matching {
		if err := c.SetWithTTL(ctx, key, "1", time.Minute); err != nil {
			t.Fatalf("SetWithTTL(%q) error: %v", key, err)
		}
	}
	if err := c.SetWithTTL(ctx, other, "1", time.Minute); err != nil {
		t.Fatalf("SetWithTTL(%q) error: %v", other, err)
	}

	if err := c.DeleteByPrefix(ctx, prefix); err != nil {
		t.Fatalf("DeleteByPrefix() error: %v", err)
	}

	for _, key := range matching {
		exists, err := c.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists(%q) error: %v", key, err)
		}
		if exists {
			t.Errorf("Exists(%q) = true after DeleteByPrefix, want false", key)
		}
	}

	exists, err := c.Exists(ctx, other)
	if err != nil {
		t.Fatalf("Exists(%q) error: %v", other, err)
	}
	if !exists {
		t.Errorf("Exists(%q) = false after DeleteByPrefix of a different prefix, want true", other)
	}
}

func TestDeleteByPrefix_NoMatchesIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	defer func() { _ = c.Close() }()

	if err := c.DeleteByPrefix(ctx, "cache-test:"+t.Name()+":nonexistent:"); err != nil {
		t.Errorf("DeleteByPrefix() with no matches: error = %v, want nil", err)
	}
}

func TestNewUsesFailoverClientWhenSentinelsConfigured(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// No real Sentinel in the dev stack — this only exercises the
	// FailoverClient branch and confirms it fails cleanly rather than
	// silently falling back to a direct client.
	_, err := New(ctx, Config{
		SentinelAddrs: []string{"127.0.0.1:1"},
		MasterName:    "mymaster",
		MaxRetries:    0,
	})
	if err == nil {
		t.Fatal("New() with unreachable Sentinel addrs: expected an error, got nil")
	}
}
