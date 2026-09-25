package loginflow

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// fill records n attempts against key directly, so a test can reach a
// limit without paying minResponseTime per login.
func fill(t *testing.T, c *cache.Client, key string, n int, window time.Duration) {
	t.Helper()
	for range n {
		if ok, _, err := c.SlidingWindowAllow(t.Context(), key, n+1, window); err != nil || !ok {
			t.Fatalf("pre-fill %s: allowed=%v err=%v", key, ok, err)
		}
	}
}

func assertRateLimited(t *testing.T, code int, header http.Header, body map[string]any) {
	t.Helper()
	if code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", code)
	}
	if secs, err := strconv.Atoi(header.Get("Retry-After")); err != nil || secs < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", header.Get("Retry-After"))
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "rate_limit_exceeded" {
		t.Errorf("error code = %v, want rate_limit_exceeded", errBody["code"])
	}
}

func TestLogin_PerEmailLimitRejectsAfterTenAttempts(t *testing.T) {
	f := newFixture(t)

	for i := range emailLimit {
		rec := f.doLogin(t, map[string]any{"email": f.email, "password": "wrong", "tenant": f.tenantSlug}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, rec.Code)
		}
	}

	// The right password no longer gets through: nothing past the limiter runs.
	rec := f.doLogin(t, map[string]any{"email": f.email, "password": testPassword, "tenant": f.tenantSlug}, nil)
	assertRateLimited(t, rec.Code, rec.Header(), decodeBody(t, rec))
}

func TestLogin_PerEmailLimitKeysOnLowercasedEmail(t *testing.T) {
	f := newFixture(t)
	fill(t, f.cache, emailKey(f.email), emailLimit, emailWindow)

	rec := f.doLogin(t, map[string]any{"email": strings.ToUpper(f.email), "password": testPassword, "tenant": f.tenantSlug}, nil)
	assertRateLimited(t, rec.Code, rec.Header(), decodeBody(t, rec))
}

func TestLogin_RateLimitedResponseIsIdenticalForUnknownEmail(t *testing.T) {
	f := newFixture(t)
	unknown := fmt.Sprintf("nobody%d@example.com", time.Now().UnixNano())
	unknownKey := emailKey(unknown)
	t.Cleanup(func() { _ = f.cache.Delete(context.Background(), unknownKey) })

	fill(t, f.cache, emailKey(f.email), emailLimit, emailWindow)
	fill(t, f.cache, unknownKey, emailLimit, emailWindow)

	known := f.doLogin(t, map[string]any{"email": f.email, "password": "wrong", "tenant": f.tenantSlug}, nil)
	missing := f.doLogin(t, map[string]any{"email": unknown, "password": "wrong", "tenant": f.tenantSlug}, nil)

	assertRateLimited(t, known.Code, known.Header(), decodeBody(t, known))
	if missing.Code != known.Code || missing.Body.String() != known.Body.String() {
		t.Errorf("unknown email response = %d %s, want identical to known email's %d %s", missing.Code, missing.Body, known.Code, known.Body)
	}
}

func tenantRateExceededEvents(t *testing.T, f *fixture) int {
	t.Helper()
	var n int
	if err := f.conn.QueryRow(`SELECT count(*) FROM system.auth_audit_log WHERE event_type = 'login.tenant_rate_exceeded' AND tenant_id = $1`, f.tenantID).Scan(&n); err != nil {
		t.Fatalf("count login.tenant_rate_exceeded rows: %v", err)
	}
	return n
}

func TestLogin_PerTenantLimitDetectsWithoutBlocking(t *testing.T) {
	f := newFixture(t)
	fill(t, f.cache, "ratelimit:login:tenant:"+f.tenantID, tenantLimit, tenantWindow)

	for i := range 3 {
		rec := f.doLogin(t, map[string]any{"email": f.email, "password": testPassword, "tenant": f.tenantSlug}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("login %d over the tenant limit: status = %d, want 200: %s", i+1, rec.Code, rec.Body)
		}
	}

	if n := tenantRateExceededEvents(t, f); n != 1 {
		t.Fatalf("login.tenant_rate_exceeded rows = %d, want 1 per tenant per window", n)
	}
	var metadata string
	if err := f.conn.QueryRow(`SELECT metadata::text FROM system.auth_audit_log WHERE event_type = 'login.tenant_rate_exceeded' AND tenant_id = $1`, f.tenantID).Scan(&metadata); err != nil {
		t.Fatalf("read event metadata: %v", err)
	}
	if metadata != `{"limit": 100, "window_seconds": 60}` {
		t.Errorf("metadata = %s, want limit 100 and window_seconds 60", metadata)
	}
}

func TestLogin_PerTenantLimitUnderThresholdWritesNoEvent(t *testing.T) {
	f := newFixture(t)

	rec := f.doLogin(t, map[string]any{"email": f.email, "password": testPassword, "tenant": f.tenantSlug}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if n := tenantRateExceededEvents(t, f); n != 0 {
		t.Errorf("login.tenant_rate_exceeded rows = %d, want 0 under the limit", n)
	}
}

func TestLogin_PerEmailRejectionIsTheOnly429UnderTenantFlood(t *testing.T) {
	f := newFixture(t)
	fill(t, f.cache, "ratelimit:login:tenant:"+f.tenantID, tenantLimit, tenantWindow)

	for i := range emailLimit {
		rec := f.doLogin(t, map[string]any{"email": f.email, "password": "wrong", "tenant": f.tenantSlug}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401 (the tenant limiter must not reject)", i+1, rec.Code)
		}
	}
	rec := f.doLogin(t, map[string]any{"email": f.email, "password": testPassword, "tenant": f.tenantSlug}, nil)
	assertRateLimited(t, rec.Code, rec.Header(), decodeBody(t, rec))
}

func TestLogin_PerIPLimitDelaysWithoutBlocking(t *testing.T) {
	f := newFixture(t)
	body := map[string]any{"email": f.email, "password": testPassword, "tenant": f.tenantSlug}

	start := time.Now()
	if rec := f.doLogin(t, body, nil); rec.Code != http.StatusOK {
		t.Fatalf("baseline status = %d, want 200: %s", rec.Code, rec.Body)
	}
	baseline := time.Since(start)

	// The baseline login already used one slot of the IP window.
	fill(t, f.cache, "ratelimit:login:ip:"+f.remoteIP, ipLimit-1, ipWindow)
	start = time.Now()
	rec := f.doLogin(t, body, nil)
	limited := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the per-IP limit must not block): %s", rec.Code, rec.Body)
	}
	// minResponseTime pads the baseline, so only its work beyond that floor
	// adds to the delay.
	want := ipOverLimitDelay + max(0, baseline-minResponseTime) - 100*time.Millisecond
	if limited < want {
		t.Errorf("over-limit login took %v against a %v baseline, want at least %v", limited, baseline, want)
	}
}

func TestLogin_UnknownTenantSkipsTenantLimiterAndStaysInvalidCredentials(t *testing.T) {
	f := newFixture(t)

	rec := f.doLogin(t, map[string]any{"email": f.email, "password": testPassword, "tenant": "no-such-tenant"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLogin_RateLimiterFailsOpenWhenRedisIsDown(t *testing.T) {
	f := newFixture(t)
	down, err := cache.New(t.Context(), cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 0})
	if err != nil {
		t.Skipf("redis not reachable: %v", err)
	}
	_ = down.Close()
	f.handler.cache = down

	rec := f.doLogin(t, map[string]any{"email": f.email, "password": testPassword, "tenant": f.tenantSlug}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with Redis unavailable: %s", rec.Code, rec.Body)
	}
}

func TestLogin_PerTenantAuditFailureReleasesGuardForRetry(t *testing.T) {
	f := newFixture(t)
	closed, err := db.New(localPostgresDSN)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	_ = closed.Close()
	working := f.handler.audit
	f.handler.audit = authaudit.NewStore(closed, tenant.NewStore(closed))
	fill(t, f.cache, "ratelimit:login:tenant:"+f.tenantID, tenantLimit, tenantWindow)

	rec := f.doLogin(t, map[string]any{"email": f.email, "password": testPassword, "tenant": f.tenantSlug}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when the audit write fails: %s", rec.Code, rec.Body)
	}
	if held, err := f.cache.Exists(t.Context(), "ratelimit:login:tenant_alerted:"+f.tenantID); err != nil || held {
		t.Fatalf("alert guard held = %v (err %v) after a failed audit write, want released", held, err)
	}

	f.handler.audit = working
	f.doLogin(t, map[string]any{"email": f.email, "password": testPassword, "tenant": f.tenantSlug}, nil)
	if n := tenantRateExceededEvents(t, f); n != 1 {
		t.Errorf("login.tenant_rate_exceeded rows after retry = %d, want 1", n)
	}
}

func TestDetectTenantFlood_WritesEventWhenRequestContextIsCanceled(t *testing.T) {
	f := newFixture(t)
	fill(t, f.cache, "ratelimit:login:tenant:"+f.tenantID, tenantLimit, tenantWindow)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.handler.detectTenantFlood(ctx, f.tenantID)

	if n := tenantRateExceededEvents(t, f); n != 1 {
		t.Errorf("login.tenant_rate_exceeded rows = %d, want 1 despite the canceled request context", n)
	}
}
