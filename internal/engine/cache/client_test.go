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
	defer func() { _ = c.Close() }()

	key := "cache-test:" + t.Name()

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
	defer func() { _ = c.Close() }()

	key := "cache-test:" + t.Name()
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
	defer func() { _ = c.Close() }()

	key := "cache-test:" + t.Name()
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
