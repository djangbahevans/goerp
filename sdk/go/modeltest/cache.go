package modeltest

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
)

const defaultTestRedisAddr = "localhost:6379"

func testRedisAddr() string {
	if addr := os.Getenv("GOERP_TEST_REDIS_ADDR"); addr != "" {
		return addr
	}
	return defaultTestRedisAddr
}

// failer is the slice of *testing.T the assertions use, so their failure
// paths can be tested without failing the test that exercises them.
type failer interface {
	Helper()
	Fatalf(format string, args ...any)
}

// TestCache is h.Cache — assertions against the module's cache definitions
// (§8 "Cache"). The module's host.cache calls run against a real Redis with
// the production {tenant_id}:{module_name}:{key} namespacing; the harness's
// tenant ID is unique per test, so tests never see each other's entries.
type TestCache struct {
	t         failer
	client    *cache.Client
	redisAddr string
	namespace string

	mu       sync.Mutex
	prefixes []string
}

// openTestRedis connects the harness to Redis. A Redis that is not reachable
// leaves the module's cache degraded, as it is in production, and makes h.Cache
// assertions fail with that reason rather than skipping every test of a module
// that never uses a cache. The returned client is nil in that case.
func openTestRedis(t *testing.T) *cache.Client {
	t.Helper()

	addr := testRedisAddr()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	client, err := cache.New(ctx, cache.Config{Addr: addr, MaxRetries: 1})
	if err != nil {
		t.Logf("modeltest: redis not reachable at %s (start compose.dev.yml, or set GOERP_TEST_REDIS_ADDR); the module's cache is degraded and h.Cache assertions fail: %v", addr, err)
		return nil
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// newTestCache builds h.Cache over client, which may be nil.
func newTestCache(t failer, client *cache.Client, tenantID, moduleName string) *TestCache {
	tc := &TestCache{t: t, client: client, redisAddr: testRedisAddr(), namespace: tenantID + ":" + moduleName + ":"}
	if client != nil {
		client.ObserveDeletes(tc.observeDelete)
	}
	return tc
}

// observeDelete records a prefix deletion inside the module's namespace. A
// single-key delete is not an invalidation of the whole cache and is not
// recorded.
func (c *TestCache) observeDelete(keyOrPrefix string, isPrefix bool) {
	rel, ok := strings.CutPrefix(keyOrPrefix, c.namespace)
	if !ok || !isPrefix {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prefixes = append(c.prefixes, rel)
}

func (c *TestCache) requireRedis() {
	c.t.Helper()
	if c.client == nil {
		c.t.Fatalf("modeltest: h.Cache needs Redis, which is not reachable at %s (start compose.dev.yml, or set GOERP_TEST_REDIS_ADDR)", c.redisAddr)
	}
}

// AssertSet fails the test unless the entry of def for args is set. def is a
// cache.Cache or cache.LoadingCache, so the key is built by the definition and
// cannot drift from the one the module uses.
func (c *TestCache) AssertSet[A any](def interface{ Key(A) string }, args A) {
	c.t.Helper()
	c.requireRedis()

	key := def.Key(args)
	set, err := c.client.Exists(context.Background(), c.namespace+key)
	if err != nil {
		c.t.Fatalf("modeltest: check cache entry %q: %v", key, err)
	}
	if !set {
		c.t.Fatalf("modeltest: cache entry %q is not set", key)
	}
}

// AssertInvalidated fails the test unless def's InvalidateAll ran during the
// test. Other caches' invalidations do not count.
func (c *TestCache) AssertInvalidated(def interface{ Name() string }) {
	c.t.Helper()
	c.requireRedis()

	c.mu.Lock()
	defer c.mu.Unlock()
	if slices.Contains(c.prefixes, def.Name()+":") {
		return
	}
	c.t.Fatalf("modeltest: cache %q was not invalidated (InvalidateAll calls seen for: %s)", def.Name(), describePrefixes(c.prefixes))
}

func describePrefixes(prefixes []string) string {
	if len(prefixes) == 0 {
		return "no cache"
	}
	return fmt.Sprintf("%q", prefixes)
}
