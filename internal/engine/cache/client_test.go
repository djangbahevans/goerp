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
