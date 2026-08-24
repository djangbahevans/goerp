package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/route"
)

const rateLimitTestRedisAddr = "localhost:6379"

func newRateLimitTestCacheClient(t *testing.T) *cache.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := cache.New(ctx, cache.Config{Addr: rateLimitTestRedisAddr, DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at %s (start compose.dev.yml): %v", rateLimitTestRedisAddr, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRateLimitMiddleware_AllowsWithinLimitThenRejects(t *testing.T) {
	redisClient := newRateLimitTestCacheClient(t)
	defaultCfg := route.RateLimitConfig{Requests: 2, WindowSeconds: 60, Scope: "ip"}
	h := rateLimitMiddleware(redisClient, defaultCfg)(okHandler())

	ip := fmt.Sprintf("203.0.113.%d", time.Now().UnixNano()%250+1)
	defer func() { _ = redisClient.Delete(context.Background(), "ratelimit:default:ip:"+ip) }()

	for i := range 2 {
		req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	req.RemoteAddr = ip
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header on a 429 response")
	}

	var body routeErrorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "rate_limit_exceeded" {
		t.Errorf("error.code = %q, want %q", body.Error.Code, "rate_limit_exceeded")
	}
}

func TestRateLimitMiddleware_DifferentIPsHaveIndependentBuckets(t *testing.T) {
	redisClient := newRateLimitTestCacheClient(t)
	defaultCfg := route.RateLimitConfig{Requests: 1, WindowSeconds: 60, Scope: "ip"}
	h := rateLimitMiddleware(redisClient, defaultCfg)(okHandler())

	suffix := time.Now().UnixNano() % 250
	ipA := fmt.Sprintf("203.0.114.%d", suffix)
	ipB := fmt.Sprintf("203.0.115.%d", suffix)
	defer func() {
		_ = redisClient.Delete(context.Background(), "ratelimit:default:ip:"+ipA)
		_ = redisClient.Delete(context.Background(), "ratelimit:default:ip:"+ipB)
	}()

	for _, ip := range []string{ipA, ipB} {
		req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("first request from %s: status = %d, want 200", ip, w.Code)
		}
	}
}

func TestRateLimitMiddleware_RouteDeclaredLimitOverridesDefault(t *testing.T) {
	redisClient := newRateLimitTestCacheClient(t)
	// A generous default that would never itself trigger a 429 within
	// this test, proving the route's own stricter RouteManifest.RateLimit
	// is what's actually enforced.
	defaultCfg := route.RateLimitConfig{Requests: 10000, WindowSeconds: 60, Scope: "ip"}
	h := rateLimitMiddleware(redisClient, defaultCfg)(okHandler())

	rr := &routeResolution{entry: &route.RouteEntry{
		PathTemplate: "/widgets/expensive",
		Manifest:     route.RouteManifest{RateLimit: &route.RateLimitConfig{Requests: 1, WindowSeconds: 60, Scope: "ip"}},
	}}

	ip := fmt.Sprintf("203.0.116.%d", time.Now().UnixNano()%250+1)
	defer func() { _ = redisClient.Delete(context.Background(), "ratelimit:route:/widgets/expensive:ip:"+ip) }()

	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/widgets/expensive", nil)
		req.RemoteAddr = ip
		return req.WithContext(withRouteResolution(req.Context(), rr))
	}

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, newReq())
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200", w1.Code)
	}

	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, newReq())
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429 (route's own limit of 1 should apply, not the generous default)", w2.Code)
	}
}

func TestRateLimitMiddleware_RouteAndDefaultBucketsAreIndependent(t *testing.T) {
	redisClient := newRateLimitTestCacheClient(t)
	defaultCfg := route.RateLimitConfig{Requests: 1, WindowSeconds: 60, Scope: "ip"}
	h := rateLimitMiddleware(redisClient, defaultCfg)(okHandler())

	rr := &routeResolution{entry: &route.RouteEntry{
		PathTemplate: "/widgets/expensive",
		Manifest:     route.RouteManifest{RateLimit: &route.RateLimitConfig{Requests: 1, WindowSeconds: 60, Scope: "ip"}},
	}}

	ip := fmt.Sprintf("203.0.117.%d", time.Now().UnixNano()%250+1)
	defer func() {
		_ = redisClient.Delete(context.Background(), "ratelimit:route:/widgets/expensive:ip:"+ip)
		_ = redisClient.Delete(context.Background(), "ratelimit:default:ip:"+ip)
	}()

	// Exhaust the route-specific bucket.
	req1 := httptest.NewRequest(http.MethodGet, "/widgets/expensive", nil)
	req1.RemoteAddr = ip
	req1 = req1.WithContext(withRouteResolution(req1.Context(), rr))
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("route-scoped request: status = %d, want 200", w1.Code)
	}

	// A request against a route using the shared default bucket, same
	// IP, should still be allowed — the route-specific bucket being full
	// must not bleed into the default bucket.
	req2 := httptest.NewRequest(http.MethodGet, "/widgets/plain", nil)
	req2.RemoteAddr = ip
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("default-scoped request: status = %d, want 200 (independent bucket from the route-specific one)", w2.Code)
	}
}

func TestRateLimitMiddleware_ZeroValueConfigFailsOpen(t *testing.T) {
	redisClient := newRateLimitTestCacheClient(t)
	h := rateLimitMiddleware(redisClient, route.RateLimitConfig{})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	req.RemoteAddr = "203.0.118.1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (a zero-value limit config should fail open, not block everything)", w.Code)
	}
}

// TestRateLimitMiddleware_RedisErrorFailsOpen forces SlidingWindowAllow to
// return an error via an already-canceled request context (the simplest
// way to make a real Redis call fail at request time, rather than
// standing up a second, unreachable cache.Client just to prove the same
// error-handling branch) and confirms the middleware fails open rather
// than blocking the request.
func TestRateLimitMiddleware_RedisErrorFailsOpen(t *testing.T) {
	redisClient := newRateLimitTestCacheClient(t)
	h := rateLimitMiddleware(redisClient, route.RateLimitConfig{Requests: 1, WindowSeconds: 60, Scope: "ip"})(okHandler())

	canceledCtx, cancelNow := context.WithCancel(context.Background())
	cancelNow()

	req := httptest.NewRequest(http.MethodGet, "/widgets", nil).WithContext(canceledCtx)
	req.RemoteAddr = "203.0.119.1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (a Redis/context error should fail open, not block the request)", w.Code)
	}
}
