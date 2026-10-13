package cache

import (
	"testing"
	"time"
	"uuid"
)

func TestSetIfEqualWithTTL(t *testing.T) {
	c, err := New(t.Context(), localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })
	key := "test:claim:" + uuid.New().String()
	if err := c.SetWithTTL(t.Context(), key, "new-owner", time.Minute); err != nil {
		t.Fatal(err)
	}
	ok, err := c.SetIfEqualWithTTL(t.Context(), key, "old-owner", "stale-response", 24*time.Hour)
	if err != nil || ok {
		t.Fatalf("stale claim=%t err=%v", ok, err)
	}
	ok, err = c.SetIfEqualWithTTL(t.Context(), key, "new-owner", "response", 24*time.Hour)
	if err != nil || !ok {
		t.Fatalf("current claim=%t err=%v", ok, err)
	}
	value, ttl, found, err := c.GetWithTTL(t.Context(), key)
	if err != nil || !found || value != "response" || ttl < 24*time.Hour-time.Minute {
		t.Fatalf("value=%q ttl=%v found=%t err=%v", value, ttl, found, err)
	}
	if err := c.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	ok, err = c.SetIfEqualWithTTL(t.Context(), key, "new-owner", "response", time.Minute)
	if err != nil || ok {
		t.Fatalf("expired claim=%t err=%v", ok, err)
	}
}
