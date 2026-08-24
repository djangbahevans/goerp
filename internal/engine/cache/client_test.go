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

func TestSlidingWindowAllow_AllowsUpToLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	defer func() { _ = c.Close() }()

	key := "cache-test:" + t.Name()
	defer func() { _ = c.Delete(ctx, key) }()

	for i := range 3 {
		allowed, _, err := c.SlidingWindowAllow(ctx, key, 3, time.Minute)
		if err != nil {
			t.Fatalf("SlidingWindowAllow() request %d error: %v", i, err)
		}
		if !allowed {
			t.Fatalf("SlidingWindowAllow() request %d = false, want true (within limit)", i)
		}
	}
}

func TestSlidingWindowAllow_RejectsOverLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	defer func() { _ = c.Close() }()

	key := "cache-test:" + t.Name()
	defer func() { _ = c.Delete(ctx, key) }()

	for i := range 2 {
		if _, _, err := c.SlidingWindowAllow(ctx, key, 2, time.Minute); err != nil {
			t.Fatalf("SlidingWindowAllow() request %d error: %v", i, err)
		}
	}

	allowed, retryAfter, err := c.SlidingWindowAllow(ctx, key, 2, time.Minute)
	if err != nil {
		t.Fatalf("SlidingWindowAllow() error: %v", err)
	}
	if allowed {
		t.Fatal("SlidingWindowAllow() = true, want false (over limit)")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retryAfter = %v, want a positive duration within the 1-minute window", retryAfter)
	}
}

func TestSlidingWindowAllow_WindowExpiryFreesASlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	defer func() { _ = c.Close() }()

	key := "cache-test:" + t.Name()
	defer func() { _ = c.Delete(ctx, key) }()

	window := 500 * time.Millisecond
	if _, _, err := c.SlidingWindowAllow(ctx, key, 1, window); err != nil {
		t.Fatalf("SlidingWindowAllow() first request error: %v", err)
	}

	allowed, _, err := c.SlidingWindowAllow(ctx, key, 1, window)
	if err != nil {
		t.Fatalf("SlidingWindowAllow() second request error: %v", err)
	}
	if allowed {
		t.Fatal("second request within the window should have been rejected")
	}

	time.Sleep(window + 100*time.Millisecond)

	allowed, _, err = c.SlidingWindowAllow(ctx, key, 1, window)
	if err != nil {
		t.Fatalf("SlidingWindowAllow() third request error: %v", err)
	}
	if !allowed {
		t.Error("request after the window expired should have been allowed — the oldest entry should have aged out")
	}
}

// TestSlidingWindowAllow_NoBurstAcrossWindowBoundary is the AC this
// ticket calls out explicitly: a fixed-window counter (IncrWithTTL's own
// approach) lets 2x the limit through across a single window-boundary
// reset — limit requests just before the boundary, limit more just after,
// both windows individually under the cap. A genuine sliding-window log
// has no boundary to burst across: this drives requests across what would
// be a fixed-window reset point and confirms the total allowed within any
// window-sized span never exceeds the limit.
func TestSlidingWindowAllow_NoBurstAcrossWindowBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	defer func() { _ = c.Close() }()

	key := "cache-test:" + t.Name()
	defer func() { _ = c.Delete(ctx, key) }()

	const limit = 2
	window := 400 * time.Millisecond

	// Exhaust the limit right away.
	for i := range limit {
		allowed, _, err := c.SlidingWindowAllow(ctx, key, limit, window)
		if err != nil {
			t.Fatalf("SlidingWindowAllow() warmup request %d error: %v", i, err)
		}
		if !allowed {
			t.Fatalf("SlidingWindowAllow() warmup request %d = false, want true", i)
		}
	}

	// A fixed-window counter keyed by wall-clock window bucket would have
	// reset by now if this sleep crossed its boundary — a sliding window
	// must not, since less than a full window has elapsed since the
	// first of the two requests above.
	time.Sleep(window / 2)

	allowed, _, err := c.SlidingWindowAllow(ctx, key, limit, window)
	if err != nil {
		t.Fatalf("SlidingWindowAllow() error: %v", err)
	}
	if allowed {
		t.Error("request at half the window elapsed should still be rejected — no fixed-window boundary to burst across")
	}
}

func TestGetHash_MissReturnsFoundFalse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	_, found, err := c.GetHash(ctx, "cache-test:"+t.Name())
	if err != nil {
		t.Fatalf("GetHash() error: %v", err)
	}
	if found {
		t.Error("expected found = false for an unset key")
	}
}

func TestCompareAndSetHash_Create_NoRequireExistsNoCheckEtag_SucceedsUnconditionally(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })

	ok, err := c.CompareAndSetHash(ctx, key, "etag", false, false, "", "data", "payload-1", "etag-1", time.Minute)
	if err != nil {
		t.Fatalf("CompareAndSetHash() error: %v", err)
	}
	if !ok {
		t.Fatal("expected the create path to succeed")
	}

	fields, found, err := c.GetHash(ctx, key)
	if err != nil {
		t.Fatalf("GetHash() error: %v", err)
	}
	if !found {
		t.Fatal("expected the hash to exist after create")
	}
	if fields["data"] != "payload-1" || fields["etag"] != "etag-1" {
		t.Errorf("fields = %+v, want data=payload-1 etag=etag-1", fields)
	}
}

func TestCompareAndSetHash_MatchingEtag_Succeeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })

	if _, err := c.CompareAndSetHash(ctx, key, "etag", false, false, "", "data", "payload-1", "etag-1", time.Minute); err != nil {
		t.Fatalf("initial create: %v", err)
	}

	ok, err := c.CompareAndSetHash(ctx, key, "etag", true, true, "etag-1", "data", "payload-2", "etag-2", time.Minute)
	if err != nil {
		t.Fatalf("CompareAndSetHash() error: %v", err)
	}
	if !ok {
		t.Fatal("expected a matching etag to succeed")
	}

	fields, _, err := c.GetHash(ctx, key)
	if err != nil {
		t.Fatalf("GetHash() error: %v", err)
	}
	if fields["data"] != "payload-2" || fields["etag"] != "etag-2" {
		t.Errorf("fields = %+v, want data=payload-2 etag=etag-2", fields)
	}
}

func TestCompareAndSetHash_MismatchedEtag_Fails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })

	if _, err := c.CompareAndSetHash(ctx, key, "etag", false, false, "", "data", "payload-1", "etag-1", time.Minute); err != nil {
		t.Fatalf("initial create: %v", err)
	}

	ok, err := c.CompareAndSetHash(ctx, key, "etag", true, true, "stale-etag", "data", "payload-2", "etag-2", time.Minute)
	if err != nil {
		t.Fatalf("CompareAndSetHash() error: %v", err)
	}
	if ok {
		t.Fatal("expected a mismatched etag to fail")
	}

	fields, _, err := c.GetHash(ctx, key)
	if err != nil {
		t.Fatalf("GetHash() error: %v", err)
	}
	if fields["data"] != "payload-1" || fields["etag"] != "etag-1" {
		t.Errorf("expected the original fields to survive a failed CAS, got %+v", fields)
	}
}

func TestCompareAndSetHash_CheckEtagOnMissingKey_Fails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()

	ok, err := c.CompareAndSetHash(ctx, key, "etag", true, true, "some-etag", "data", "payload", "etag-new", time.Minute)
	if err != nil {
		t.Fatalf("CompareAndSetHash() error: %v", err)
	}
	if ok {
		t.Fatal("expected checkEtag against a missing key to fail")
	}
}

// TestCompareAndSetHash_RequireExistsWithoutCheckEtag_SucceedsOnExistingKey
// is the shape host.orm.write's "no expectedEtag supplied, but the record
// must already exist" path uses (goerp#344) — requireExists=true,
// checkEtag=false. This is also the fix for the TOCTOU bug a Go-side
// GetHash-then-CompareAndSetHash sequence had: existence is enforced
// inside the same EVAL as the set, not as a separate round trip a
// concurrent delete could slip between.
func TestCompareAndSetHash_RequireExistsWithoutCheckEtag_SucceedsOnExistingKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })

	if _, err := c.CompareAndSetHash(ctx, key, "etag", false, false, "", "data", "payload-1", "etag-1", time.Minute); err != nil {
		t.Fatalf("initial create: %v", err)
	}

	ok, err := c.CompareAndSetHash(ctx, key, "etag", true, false, "", "data", "payload-2", "etag-2", time.Minute)
	if err != nil {
		t.Fatalf("CompareAndSetHash() error: %v", err)
	}
	if !ok {
		t.Fatal("expected requireExists=true, checkEtag=false to succeed against an existing key regardless of its stored etag")
	}

	fields, _, err := c.GetHash(ctx, key)
	if err != nil {
		t.Fatalf("GetHash() error: %v", err)
	}
	if fields["data"] != "payload-2" || fields["etag"] != "etag-2" {
		t.Errorf("fields = %+v, want data=payload-2 etag=etag-2", fields)
	}
}

func TestCompareAndSetHash_RequireExistsWithoutCheckEtag_FailsOnMissingKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()

	ok, err := c.CompareAndSetHash(ctx, key, "etag", true, false, "", "data", "payload", "etag-1", time.Minute)
	if err != nil {
		t.Fatalf("CompareAndSetHash() error: %v", err)
	}
	if ok {
		t.Fatal("expected requireExists=true against a missing key to fail even without an etag check")
	}
}

// TestCompareAndSetHash_EmptyStringEtagIsARealPrecondition proves the
// bug fix directly: a stored etag that happens to be the empty string
// (e.g. a freshly created record's initial etag, matching
// WithStandardFields()'s ” default on the Table path) is still a real
// value checkEtag can match against — it is not confusable with "no
// precondition requested," which is signaled by checkEtag=false, not by
// expectedEtag=="".
func TestCompareAndSetHash_EmptyStringEtagIsARealPrecondition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })

	// Create with an empty-string etag, matching a fresh record's real
	// initial value.
	if _, err := c.CompareAndSetHash(ctx, key, "etag", false, false, "", "data", "payload-1", "", time.Minute); err != nil {
		t.Fatalf("initial create: %v", err)
	}

	// A write asserting the wrong non-empty etag against a record whose
	// real stored etag is "" must fail.
	ok, err := c.CompareAndSetHash(ctx, key, "etag", true, true, "not-the-real-etag", "data", "payload-2", "etag-2", time.Minute)
	if err != nil {
		t.Fatalf("CompareAndSetHash() error: %v", err)
	}
	if ok {
		t.Fatal("expected checkEtag with the wrong value to fail even though the real stored etag is empty")
	}

	// Asserting the correct empty-string etag succeeds.
	ok, err = c.CompareAndSetHash(ctx, key, "etag", true, true, "", "data", "payload-2", "etag-2", time.Minute)
	if err != nil {
		t.Fatalf("CompareAndSetHash() error: %v", err)
	}
	if !ok {
		t.Fatal("expected checkEtag with the correct empty-string etag to succeed")
	}
}

func TestCompareAndSetHash_TTLExpiresKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, err := New(ctx, localRedisConfig())
	skipIfUnreachable(t, err)
	t.Cleanup(func() { _ = c.Close() })

	key := "cache-test:" + t.Name()
	t.Cleanup(func() { _ = c.Delete(context.Background(), key) })

	if _, err := c.CompareAndSetHash(ctx, key, "etag", false, false, "", "data", "payload", "etag-1", 200*time.Millisecond); err != nil {
		t.Fatalf("CompareAndSetHash() error: %v", err)
	}

	time.Sleep(400 * time.Millisecond)

	_, found, err := c.GetHash(ctx, key)
	if err != nil {
		t.Fatalf("GetHash() error: %v", err)
	}
	if found {
		t.Error("expected the key to have expired")
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
