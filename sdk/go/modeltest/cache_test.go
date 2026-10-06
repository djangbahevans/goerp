package modeltest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	sdkcache "github.com/djangbahevans/goerp/sdk/go/cache"
)

// panicFailer stops the assertion at its first failure the way testing.T's
// Fatalf does, so the code after a failed check never runs.
type panicFailer struct{ testing.TB }

type fatalMessage string

func (panicFailer) Helper() {}

func (panicFailer) Fatalf(format string, args ...any) {
	panic(fatalMessage(fmt.Sprintf(format, args...)))
}

// failure runs assertion and returns the message it failed with, or "" when it
// passed.
func failure(assertion func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			fm, ok := r.(fatalMessage)
			if !ok {
				panic(r)
			}
			msg = string(fm)
		}
	}()
	assertion()
	return ""
}

type listArgs struct{ Tenant string }

func listKey(a listArgs) string { return a.Tenant }

// Cache names are unique per process, so the definitions are shared by every
// test. "contact" is a leading substring of "contact_list" on purpose.
var (
	contactListCache   = sdkcache.Define[listArgs, []string]("contact_list", listKey, sdkcache.TTL(time.Minute))
	contactDetailCache = sdkcache.Define[listArgs, string]("contact", listKey, sdkcache.TTL(time.Minute))
)

func openRedisOrSkip(t *testing.T) *cache.Client {
	t.Helper()
	client := openTestRedis(t)
	if client == nil {
		t.Skip("redis not reachable (start compose.dev.yml, or set GOERP_TEST_REDIS_ADDR)")
	}
	return client
}

func newTestCacheForTest(t *testing.T) (*TestCache, *cache.Client) {
	t.Helper()
	client := openRedisOrSkip(t)
	tenantID := fmt.Sprintf("modeltest-cache-%s-%d", t.Name(), time.Now().UnixNano())
	tc := newTestCache(panicFailer{t}, client, tenantID, "contacts")
	t.Cleanup(func() { _ = client.DeleteByPrefix(context.Background(), tenantID+":") })
	return tc, client
}

func TestAssertSet_PassesOnlyForTheEntryThatIsSet(t *testing.T) {
	tc, client := newTestCacheForTest(t)

	if err := client.SetWithTTL(t.Context(), tc.namespace+contactListCache.Key(listArgs{Tenant: "acme"}), "v", time.Minute); err != nil {
		t.Fatalf("SetWithTTL: %v", err)
	}

	if msg := failure(func() { tc.AssertSet(contactListCache, listArgs{Tenant: "acme"}) }); msg != "" {
		t.Fatalf("AssertSet failed for a set entry: %s", msg)
	}
	msg := failure(func() { tc.AssertSet(contactListCache, listArgs{Tenant: "other"}) })
	if !strings.Contains(msg, "contact_list:other") {
		t.Errorf("failure = %q, want one naming the unset entry", msg)
	}
}

func TestAssertInvalidated_PassesOnlyForTheInvalidatedCache(t *testing.T) {
	tc, client := newTestCacheForTest(t)

	if err := client.DeleteByPrefix(t.Context(), tc.namespace+"contact_list:"); err != nil {
		t.Fatalf("DeleteByPrefix: %v", err)
	}

	if msg := failure(func() { tc.AssertInvalidated(contactListCache) }); msg != "" {
		t.Fatalf("AssertInvalidated failed for an invalidated cache: %s", msg)
	}
	// "contact:" is not a prefix-match of "contact_list:", and this cache was
	// never invalidated.
	msg := failure(func() { tc.AssertInvalidated(contactDetailCache) })
	if !strings.Contains(msg, `"contact"`) {
		t.Errorf("failure = %q, want one naming the untouched cache", msg)
	}
}

func TestAssertInvalidated_IgnoresSingleKeyDeletesAndOtherNamespaces(t *testing.T) {
	tc, client := newTestCacheForTest(t)

	_ = client.Delete(t.Context(), tc.namespace+"contact_list:acme")
	_ = client.DeleteByPrefix(t.Context(), "another-tenant:contacts:contact_list:")

	if msg := failure(func() { tc.AssertInvalidated(contactListCache) }); msg == "" {
		t.Error("AssertInvalidated passed: neither a single-key delete nor another tenant's invalidation counts")
	}
}

func TestAssertions_FailClearlyWithoutRedis(t *testing.T) {
	tc := &TestCache{t: panicFailer{t}, redisAddr: "localhost:1"}

	for name, assertion := range map[string]func(){
		"AssertSet":         func() { tc.AssertSet(contactListCache, listArgs{Tenant: "acme"}) },
		"AssertInvalidated": func() { tc.AssertInvalidated(contactListCache) },
	} {
		msg := failure(assertion)
		if !strings.Contains(msg, "needs Redis") || !strings.Contains(msg, "localhost:1") {
			t.Errorf("%s failure = %q, want it to say Redis is needed and where it looked", name, msg)
		}
	}
}
