package wasm

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/cache"
)

func newCacheTestModuleContext(tenantID, moduleName string, caps abi.CapabilitySet) *ModuleContext {
	return NewModuleContext("req-1", moduleName, "user-1", "", nil, nil, tenantID, tenantID, "trace-1", caps, nil, ModuleSnapshot{})
}

func cacheTestTenantID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("cachetest-%s-%d", t.Name(), time.Now().UnixNano())
}

// downedCacheClient returns a client whose Redis connection has been closed,
// so every call fails the way an unreachable Redis would.
func downedCacheClient(t *testing.T) *cache.Client {
	t.Helper()
	c := openTestCacheClient(t)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return c
}

func mustCacheSet(t *testing.T, c *cache.Client, mc *ModuleContext, key string, value []byte, ttlSeconds int64) {
	t.Helper()
	if _, hostErr := CacheSet(t.Context(), c, mc, abiv1.CacheSetInput{Key: key, Value: value, TTLSeconds: ttlSeconds}); hostErr != nil {
		t.Fatalf("CacheSet(%q): %v", key, hostErr)
	}
}

func cacheLookup(t *testing.T, c *cache.Client, mc *ModuleContext, key string) abiv1.CacheGetOutput {
	t.Helper()
	out, hostErr := CacheGet(t.Context(), c, mc, abiv1.CacheGetInput{Key: key})
	if hostErr != nil {
		t.Fatalf("CacheGet(%q): %v", key, hostErr)
	}
	return out
}

func TestHostCache_SetThenGet_RoundTripsWithRemainingTTL(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	t.Cleanup(func() { _, _ = CacheInvalidatePrefix(context.Background(), c, mc, abiv1.CacheInvalidatePrefixInput{}) })

	value := []byte{0x81, 0xa1, 'k', 0x01}
	mustCacheSet(t, c, mc, "contact:1", value, 60)

	got := cacheLookup(t, c, mc, "contact:1")
	if !got.Found || !bytes.Equal(got.Value, value) {
		t.Fatalf("got Found=%v Value=%x, want Found=true Value=%x", got.Found, got.Value, value)
	}
	if got.TTLRemainingMS == nil || *got.TTLRemainingMS <= 0 || *got.TTLRemainingMS > 60_000 {
		t.Errorf("TTLRemainingMS = %v, want within (0, 60000]", got.TTLRemainingMS)
	}
}

func TestHostCache_Get_MissIsNotFound(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead)

	got := cacheLookup(t, c, mc, "absent")
	if got.Found || got.Value != nil || got.TTLRemainingMS != nil {
		t.Errorf("got %+v, want zero output", got)
	}
}

func TestHostCache_ZeroTTL_NeverExpiresAndReportsNoRemainingTTL(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	t.Cleanup(func() { _, _ = CacheInvalidatePrefix(context.Background(), c, mc, abiv1.CacheInvalidatePrefixInput{}) })

	mustCacheSet(t, c, mc, "forever", []byte("v"), 0)

	got := cacheLookup(t, c, mc, "forever")
	if !got.Found || got.TTLRemainingMS != nil {
		t.Errorf("got Found=%v TTLRemainingMS=%v, want Found=true TTLRemainingMS=nil", got.Found, got.TTLRemainingMS)
	}
}

func TestHostCache_TTLExpiry_RemovesEntry(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)

	mustCacheSet(t, c, mc, "short", []byte("v"), 1)
	if !cacheLookup(t, c, mc, "short").Found {
		t.Fatal("entry missing before its TTL elapsed")
	}

	deadline := time.Now().Add(3 * time.Second)
	for cacheLookup(t, c, mc, "short").Found {
		if time.Now().After(deadline) {
			t.Fatal("entry still present after its 1s TTL")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestHostCache_Isolation_AcrossModulesAndTenants(t *testing.T) {
	c := openTestCacheClient(t)
	tenantA := cacheTestTenantID(t) + "-a"
	tenantB := cacheTestTenantID(t) + "-b"
	caps := abi.CapCacheRead | abi.CapCacheWrite
	contactsA := newCacheTestModuleContext(tenantA, "contacts", caps)
	billingA := newCacheTestModuleContext(tenantA, "billing", caps)
	contactsB := newCacheTestModuleContext(tenantB, "contacts", caps)
	for _, mc := range []*ModuleContext{contactsA, billingA, contactsB} {
		t.Cleanup(func() { _, _ = CacheInvalidatePrefix(context.Background(), c, mc, abiv1.CacheInvalidatePrefixInput{}) })
	}

	mustCacheSet(t, c, contactsA, "k", []byte("a"), 60)

	if got := cacheLookup(t, c, contactsA, "k"); !got.Found || string(got.Value) != "a" {
		t.Errorf("owner read = %+v, want its own value", got)
	}
	if got := cacheLookup(t, c, billingA, "k"); got.Found {
		t.Error("another module in the same tenant saw the entry")
	}
	if got := cacheLookup(t, c, contactsB, "k"); got.Found {
		t.Error("the same module in another tenant saw the entry")
	}
}

func TestHostCache_Delete_RemovesOnlyThatKey(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	t.Cleanup(func() { _, _ = CacheInvalidatePrefix(context.Background(), c, mc, abiv1.CacheInvalidatePrefixInput{}) })

	mustCacheSet(t, c, mc, "a", []byte("1"), 60)
	mustCacheSet(t, c, mc, "b", []byte("2"), 60)

	if _, hostErr := CacheDelete(t.Context(), c, mc, abiv1.CacheDeleteInput{Key: "a"}); hostErr != nil {
		t.Fatalf("CacheDelete: %v", hostErr)
	}
	if cacheLookup(t, c, mc, "a").Found {
		t.Error("deleted key still present")
	}
	if !cacheLookup(t, c, mc, "b").Found {
		t.Error("unrelated key was deleted")
	}
}

func TestHostCache_InvalidatePrefix_DeletesOnlyMatchesInOwnNamespace(t *testing.T) {
	c := openTestCacheClient(t)
	tenant := cacheTestTenantID(t)
	caps := abi.CapCacheRead | abi.CapCacheWrite
	contacts := newCacheTestModuleContext(tenant, "contacts", caps)
	billing := newCacheTestModuleContext(tenant, "billing", caps)
	otherTenant := newCacheTestModuleContext(tenant+"-other", "contacts", caps)
	for _, mc := range []*ModuleContext{contacts, billing, otherTenant} {
		t.Cleanup(func() { _, _ = CacheInvalidatePrefix(context.Background(), c, mc, abiv1.CacheInvalidatePrefixInput{}) })
	}

	for _, mc := range []*ModuleContext{contacts, billing, otherTenant} {
		mustCacheSet(t, c, mc, "list:1", []byte("x"), 60)
		mustCacheSet(t, c, mc, "list:2", []byte("x"), 60)
		mustCacheSet(t, c, mc, "detail:1", []byte("x"), 60)
	}

	if _, hostErr := CacheInvalidatePrefix(t.Context(), c, contacts, abiv1.CacheInvalidatePrefixInput{Prefix: "list:"}); hostErr != nil {
		t.Fatalf("CacheInvalidatePrefix: %v", hostErr)
	}

	for _, key := range []string{"list:1", "list:2"} {
		if cacheLookup(t, c, contacts, key).Found {
			t.Errorf("%q survived invalidation in the caller's namespace", key)
		}
	}
	if !cacheLookup(t, c, contacts, "detail:1").Found {
		t.Error("a key outside the prefix was deleted")
	}
	for name, mc := range map[string]*ModuleContext{"another module": billing, "another tenant": otherTenant} {
		if !cacheLookup(t, c, mc, "list:1").Found {
			t.Errorf("invalidation reached %s", name)
		}
	}
}

func TestHostCache_InvalidatePrefix_TreatsGlobCharactersLiterally(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	t.Cleanup(func() { _, _ = CacheInvalidatePrefix(context.Background(), c, mc, abiv1.CacheInvalidatePrefixInput{}) })

	mustCacheSet(t, c, mc, "a*b", []byte("x"), 60)
	mustCacheSet(t, c, mc, "axb", []byte("x"), 60)

	if _, hostErr := CacheInvalidatePrefix(t.Context(), c, mc, abiv1.CacheInvalidatePrefixInput{Prefix: "a*"}); hostErr != nil {
		t.Fatalf("CacheInvalidatePrefix: %v", hostErr)
	}
	if cacheLookup(t, c, mc, "a*b").Found {
		t.Error("the literal match survived")
	}
	if !cacheLookup(t, c, mc, "axb").Found {
		t.Error("'*' was treated as a wildcard")
	}
}

func TestHostCache_RedisDown_DegradesWithoutFailing(t *testing.T) {
	for name, c := range map[string]*cache.Client{"closed client": downedCacheClient(t), "nil client": nil} {
		t.Run(name, func(t *testing.T) {
			mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)

			if got := cacheLookup(t, c, mc, "k"); got.Found {
				t.Error("get reported a hit with Redis down")
			}
			mustCacheSet(t, c, mc, "k", []byte("v"), 60)
			if _, hostErr := CacheDelete(t.Context(), c, mc, abiv1.CacheDeleteInput{Key: "k"}); hostErr != nil {
				t.Errorf("delete: %v", hostErr)
			}
			if _, hostErr := CacheInvalidatePrefix(t.Context(), c, mc, abiv1.CacheInvalidatePrefixInput{Prefix: "k"}); hostErr != nil {
				t.Errorf("invalidate_prefix: %v", hostErr)
			}
		})
	}
}

func TestHostCache_RequiresCapabilities(t *testing.T) {
	c := openTestCacheClient(t)
	tenant := cacheTestTenantID(t)
	readOnly := newCacheTestModuleContext(tenant, "contacts", abi.CapCacheRead)
	writeOnly := newCacheTestModuleContext(tenant, "contacts", abi.CapCacheWrite)

	if _, hostErr := CacheGet(t.Context(), c, writeOnly, abiv1.CacheGetInput{Key: "k"}); hostErr == nil || hostErr.Code != abiv1.ErrCodeCapabilityDenied {
		t.Errorf("get without cache.read: %v, want capability denied", hostErr)
	}
	if _, hostErr := CacheSet(t.Context(), c, readOnly, abiv1.CacheSetInput{Key: "k", Value: []byte("v")}); hostErr == nil || hostErr.Code != abiv1.ErrCodeCapabilityDenied {
		t.Errorf("set without cache.write: %v, want capability denied", hostErr)
	}
	if _, hostErr := CacheDelete(t.Context(), c, readOnly, abiv1.CacheDeleteInput{Key: "k"}); hostErr == nil || hostErr.Code != abiv1.ErrCodeCapabilityDenied {
		t.Errorf("delete without cache.write: %v, want capability denied", hostErr)
	}
	if _, hostErr := CacheInvalidatePrefix(t.Context(), c, readOnly, abiv1.CacheInvalidatePrefixInput{}); hostErr == nil || hostErr.Code != abiv1.ErrCodeCapabilityDenied {
		t.Errorf("invalidate_prefix without cache.write: %v, want capability denied", hostErr)
	}
}

func TestHostCache_Set_RejectsOversizedValueAndInvalidTTL(t *testing.T) {
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheWrite)

	_, hostErr := CacheSet(t.Context(), nil, mc, abiv1.CacheSetInput{Key: "k", Value: make([]byte, cacheMaxValueBytes+1)})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeCacheValueTooLarge {
		t.Errorf("oversized value: %v, want %s", hostErr, abiv1.ErrCodeCacheValueTooLarge)
	}

	for _, ttl := range []int64{-1, cacheMaxTTLSeconds + 1, math.MaxInt64} {
		_, hostErr = CacheSet(t.Context(), nil, mc, abiv1.CacheSetInput{Key: "k", Value: []byte("v"), TTLSeconds: ttl})
		if hostErr == nil || hostErr.Code != abiv1.ErrCodeCacheInvalidTTL {
			t.Errorf("ttl %d: %v, want %s", ttl, hostErr, abiv1.ErrCodeCacheInvalidTTL)
		}
	}
}

type countingLoader struct {
	calls atomic.Int32
	delay time.Duration
	value []byte
	err   *abiv1.HostError
}

func (l *countingLoader) load(context.Context, *ModuleContext, abiv1.CacheGetOrSetInput) ([]byte, *abiv1.HostError) {
	l.calls.Add(1)
	time.Sleep(l.delay)
	return l.value, l.err
}

func cleanupCacheNamespace(t *testing.T, c *cache.Client, mc *ModuleContext) {
	t.Helper()
	t.Cleanup(func() { _, _ = CacheInvalidatePrefix(context.Background(), c, mc, abiv1.CacheInvalidatePrefixInput{}) })
}

func getOrSetInput(key string) abiv1.CacheGetOrSetInput {
	return abiv1.CacheGetOrSetInput{Key: key, TTLSeconds: 60, LoaderFnName: "loader", LoaderArgs: []byte("args")}
}

func TestHostCache_GetOrSet_ConcurrentCallersRunLoaderOnce(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	cleanupCacheNamespace(t, c, mc)

	loader := &countingLoader{delay: 200 * time.Millisecond, value: []byte("loaded")}

	const callers = 8
	results := make([]abiv1.CacheGetOrSetOutput, callers)
	errs := make([]*abiv1.HostError, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			results[i], errs[i] = CacheGetOrSet(t.Context(), c, mc, getOrSetInput("k"), loader.load)
		})
	}
	wg.Wait()

	if got := loader.calls.Load(); got != 1 {
		t.Errorf("loader ran %d times, want 1", got)
	}
	for i := range callers {
		if errs[i] != nil || string(results[i].Value) != "loaded" {
			t.Errorf("caller %d got %q, %v; want \"loaded\", nil", i, results[i].Value, errs[i])
		}
	}
}

func TestHostCache_GetOrSet_HitSkipsLoader(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	cleanupCacheNamespace(t, c, mc)
	mustCacheSet(t, c, mc, "k", []byte("stored"), 60)

	loader := &countingLoader{value: []byte("loaded")}
	out, hostErr := CacheGetOrSet(t.Context(), c, mc, getOrSetInput("k"), loader.load)
	if hostErr != nil || string(out.Value) != "stored" {
		t.Fatalf("got %q, %v; want \"stored\", nil", out.Value, hostErr)
	}
	if got := loader.calls.Load(); got != 0 {
		t.Errorf("loader ran %d times on a hit, want 0", got)
	}
}

func TestHostCache_GetOrSet_MissStoresValueWithTTL(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	cleanupCacheNamespace(t, c, mc)

	loader := &countingLoader{value: []byte("loaded")}
	if _, hostErr := CacheGetOrSet(t.Context(), c, mc, getOrSetInput("k"), loader.load); hostErr != nil {
		t.Fatalf("CacheGetOrSet: %v", hostErr)
	}

	got := cacheLookup(t, c, mc, "k")
	if !got.Found || string(got.Value) != "loaded" || got.TTLRemainingMS == nil {
		t.Errorf("stored entry = %+v, want \"loaded\" with a TTL", got)
	}
}

func TestHostCache_GetOrSet_LoaderErrorIsReturnedAndNothingCached(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	cleanupCacheNamespace(t, c, mc)

	loader := &countingLoader{err: &abiv1.HostError{Code: "contacts.load_failed", Message: "boom"}}
	_, hostErr := CacheGetOrSet(t.Context(), c, mc, getOrSetInput("k"), loader.load)
	if hostErr == nil || hostErr.Code != "contacts.load_failed" {
		t.Fatalf("hostErr = %v, want the loader's error", hostErr)
	}
	if cacheLookup(t, c, mc, "k").Found {
		t.Error("a failed load left the key set")
	}

	loader.err, loader.value = nil, []byte("recovered")
	out, hostErr := CacheGetOrSet(t.Context(), c, mc, getOrSetInput("k"), loader.load)
	if hostErr != nil || string(out.Value) != "recovered" {
		t.Errorf("retry after failure got %q, %v; want the lock released and \"recovered\"", out.Value, hostErr)
	}
}

func TestHostCache_GetOrSet_RedisDownRunsLoaderEveryCall(t *testing.T) {
	for name, c := range map[string]*cache.Client{"closed client": downedCacheClient(t), "nil client": nil} {
		t.Run(name, func(t *testing.T) {
			mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
			loader := &countingLoader{value: []byte("loaded")}

			for range 2 {
				out, hostErr := CacheGetOrSet(t.Context(), c, mc, getOrSetInput("k"), loader.load)
				if hostErr != nil || string(out.Value) != "loaded" {
					t.Fatalf("got %q, %v; want \"loaded\", nil", out.Value, hostErr)
				}
			}
			if got := loader.calls.Load(); got != 2 {
				t.Errorf("loader ran %d times, want 2 (nothing cached)", got)
			}
		})
	}
}

func TestHostCache_GetOrSet_WaiterTimesOutWhileLockIsHeld(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	cleanupCacheNamespace(t, c, mc)

	lockKey := "cacheload:" + cacheKeyPrefix(mc) + "k"
	if _, err := c.SetNXWithTTL(t.Context(), lockKey, "other", time.Minute); err != nil {
		t.Fatalf("SetNXWithTTL: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), lockKey) })

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	loader := &countingLoader{value: []byte("loaded")}
	_, hostErr := CacheGetOrSet(ctx, c, mc, getOrSetInput("k"), loader.load)
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeTimeout {
		t.Errorf("hostErr = %v, want %s", hostErr, abiv1.ErrCodeTimeout)
	}
	if got := loader.calls.Load(); got != 0 {
		t.Errorf("loader ran %d times while another caller held the lock, want 0", got)
	}
}

// A caller whose deadline has already passed gets a timeout, not the loader:
// the failed cache call is its own expired context, not a Redis outage.
func TestHostCache_GetOrSet_ExpiredContextTimesOutWithoutRunningTheLoader(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	cleanupCacheNamespace(t, c, mc)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	loader := &countingLoader{value: []byte("loaded")}
	_, hostErr := CacheGetOrSet(ctx, c, mc, getOrSetInput("k"), loader.load)
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeTimeout {
		t.Errorf("hostErr = %v, want %s", hostErr, abiv1.ErrCodeTimeout)
	}
	if got := loader.calls.Load(); got != 0 {
		t.Errorf("loader ran %d times for an expired context, want 0", got)
	}
}

func TestHostCache_GetOrSet_RequiresBothCapabilities(t *testing.T) {
	c := openTestCacheClient(t)
	loader := &countingLoader{value: []byte("loaded")}
	for name, caps := range map[string]abi.CapabilitySet{"read only": abi.CapCacheRead, "write only": abi.CapCacheWrite} {
		mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", caps)
		if _, hostErr := CacheGetOrSet(t.Context(), c, mc, getOrSetInput("k"), loader.load); hostErr == nil || hostErr.Code != abiv1.ErrCodeCapabilityDenied {
			t.Errorf("%s: %v, want capability denied", name, hostErr)
		}
	}
	if got := loader.calls.Load(); got != 0 {
		t.Errorf("loader ran %d times without capabilities, want 0", got)
	}
}

func TestHostCache_GetOrSet_RejectsOversizedLoadedValue(t *testing.T) {
	c := openTestCacheClient(t)
	mc := newCacheTestModuleContext(cacheTestTenantID(t), "contacts", abi.CapCacheRead|abi.CapCacheWrite)
	cleanupCacheNamespace(t, c, mc)

	loader := &countingLoader{value: make([]byte, cacheMaxValueBytes+1)}
	_, hostErr := CacheGetOrSet(t.Context(), c, mc, getOrSetInput("k"), loader.load)
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeCacheValueTooLarge {
		t.Errorf("hostErr = %v, want %s", hostErr, abiv1.ErrCodeCacheValueTooLarge)
	}
}

func compileCacheLoaderFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "cacheloaderfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/cacheloaderfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/cacheloaderfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// TestHostCache_GetOrSet_RunsLoaderInRealModule drives the host function's
// loader callback through a real WASM module's handle_cache_loader export on
// a pooled instance separate from the caller's.
func TestHostCache_GetOrSet_RunsLoaderInRealModule(t *testing.T) {
	ctx := t.Context()
	c := openTestCacheClient(t)
	r := newTestRuntime(t, 64<<20)

	compiled, err := r.CompileModule(ctx, compileCacheLoaderFixture(t))
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	pool := r.NewPool("contacts", compiled, PoolConfig{})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 5*time.Second) })

	mc := NewModuleContext("req-1", "contacts", "user-1", "", nil, nil, cacheTestTenantID(t), "tenant-slug", "trace-1",
		abi.CapCacheRead|abi.CapCacheWrite, nil, ModuleSnapshot{
			ComputeTargets: map[string]ComputeTarget{"contacts": {Pool: pool}},
		})
	cleanupCacheNamespace(t, c, mc)
	load := moduleCacheLoader(r)

	out, hostErr := CacheGetOrSet(ctx, c, mc, abiv1.CacheGetOrSetInput{Key: "k", TTLSeconds: 60, LoaderFnName: "upper", LoaderArgs: []byte("abc")}, load)
	if hostErr != nil || string(out.Value) != "ABC" {
		t.Fatalf("got %q, %v; want \"ABC\", nil", out.Value, hostErr)
	}
	if got := cacheLookup(t, c, mc, "k"); !got.Found || string(got.Value) != "ABC" {
		t.Errorf("stored entry = %+v, want \"ABC\"", got)
	}

	_, hostErr = CacheGetOrSet(ctx, c, mc, abiv1.CacheGetOrSetInput{Key: "bad", TTLSeconds: 60, LoaderFnName: "fail"}, load)
	if hostErr == nil || hostErr.Code != "contacts.load_failed" {
		t.Errorf("hostErr = %v, want the module loader's error", hostErr)
	}
	if cacheLookup(t, c, mc, "bad").Found {
		t.Error("a failed load left the key set")
	}
}
