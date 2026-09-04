package authlogout

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fixture mirrors authme's own fixture shape — this handler is the same
// "already-authenticated session" caller as GET /auth/me, just mutating
// instead of reading.
type fixture struct {
	handler    *Handler
	issuer     *authtoken.Issuer
	revoker    *sessionrevoke.Revoker
	apiKeys    *apikey.Store
	domain     string
	tenantID   string
	tenantSlug string
	userID     string
	conn       *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockSharedKeyTable(t, conn)

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}
	sessionStore := session.NewStore(conn)
	if err := sessionStore.Bootstrap(ctx); err != nil {
		t.Fatalf("session Bootstrap() error: %v", err)
	}
	roleStore := role.NewStore(conn)
	apiKeys := apikey.NewStore(conn)
	if err := apiKeys.Bootstrap(ctx); err != nil {
		t.Fatalf("apikey Bootstrap() error: %v", err)
	}
	billingStore := billing.NewStore(conn)
	if err := billingStore.Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}

	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	signingKeySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("signingkey LoadOrGenerate() error: %v", err)
	}

	tenantResolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)
	issuer := authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore)
	roleCache := permcache.NewRoleCache(cacheClient)
	roleMap := permcache.NewRolePermissionMap()
	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	authChecker := authcheck.NewChecker(&signingKeySet.Active, revoker, userStore, roleStore, roleCache, roleMap, apiKeys, true, nil, nil, nil)
	handler := NewHandler(tenantResolver, authChecker, revoker)

	slug := fmt.Sprintf("authlogouttest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Auth Logout Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
	if _, err := tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate fixture tenant: %v", err)
	}

	domain := slug + ".goerp.test"
	if _, err := tenantStore.CreateDomain(ctx, tt.ID, domain, tenant.DomainSubdomain, true); err != nil {
		t.Fatalf("CreateDomain() error: %v", err)
	}

	email := slug + "@example.com"
	userID, err := userStore.FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	if _, err := conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })

	schema := tenantschema.Name(slug)
	if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)) })
	if err := roleStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roleStore.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := roleStore.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	if _, err := conn.Exec(fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID); err != nil {
		t.Fatalf("grant admin role: %v", err)
	}

	return &fixture{
		handler:    handler,
		issuer:     issuer,
		revoker:    revoker,
		apiKeys:    apiKeys,
		domain:     domain,
		tenantID:   tt.ID,
		tenantSlug: slug,
		userID:     userID,
		conn:       conn,
	}
}

// lockSharedKeyTable mirrors authme's own lock helper — serializes this
// package's tests against every other package's test touching the same
// shared signing-key table.
func lockSharedKeyTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	key := db.AdvisoryLockKey("test.jwt_signing_keys_table")
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire dedicated connection for signing-key lock: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatalf("acquire signing-key advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		_ = conn.Close()
	})
}

func (f *fixture) issueAccessToken(t *testing.T) string {
	t.Helper()
	tokens, err := f.issuer.Issue(context.Background(), authtoken.LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func (f *fixture) doLogout(t *testing.T, host, accessToken string, nonBrowser bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.Host = host
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if nonBrowser {
		req.Header.Set("X-Client-Type", "cli")
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) issueAPIKey(t *testing.T) string {
	t.Helper()
	fullKey, _, err := f.apiKeys.IssueKey(context.Background(), f.tenantID, &f.userID, "test key", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}
	return fullKey
}

func (f *fixture) sessionID(t *testing.T) string {
	t.Helper()
	var sessionID string
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT id FROM system.sessions WHERE user_id = $1`, f.userID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("query fixture session id: %v", err)
	}
	return sessionID
}

func TestServeHTTP_ValidTokenRevokesSessionAndClearsCookies(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)
	sessionID := f.sessionID(t)

	rec := f.doLogout(t, f.domain, accessToken, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Errorf("response = %v, want ok: true", resp)
	}

	blocked, err := f.revoker.IsBlocked(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("IsBlocked() error: %v", err)
	}
	if !blocked {
		t.Error("session not blocklisted after logout")
	}

	cookies := rec.Result().Cookies()
	cleared := map[string]bool{}
	for _, c := range cookies {
		if c.MaxAge < 0 {
			cleared[c.Name] = true
		}
	}
	if !cleared["__Host-access_token"] {
		t.Error("__Host-access_token cookie not cleared")
	}
	if !cleared["refresh_token"] {
		t.Error("refresh_token cookie not cleared")
	}
	if cleared["device_id"] {
		t.Error("device_id cookie was cleared, want it left alone (device identity survives logout)")
	}
}

func TestServeHTTP_NonBrowserClientGetsNoCookies(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doLogout(t, f.domain, accessToken, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("cookies = %v, want none for a non-browser client", rec.Result().Cookies())
	}
}

func TestServeHTTP_APIKeyAuthRejected(t *testing.T) {
	f := newFixture(t)
	key := f.issueAPIKey(t)

	rec := f.doLogout(t, f.domain, key, false)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "api_key_no_session") {
		t.Errorf("body = %s, want it to mention api_key_no_session", rec.Body.String())
	}
}

func TestServeHTTP_NoTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doLogout(t, f.domain, "", false)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_MalformedTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doLogout(t, f.domain, "not-a-real-token", false)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_UnresolvableHostRejected(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doLogout(t, "no-such-tenant.goerp.test", accessToken, false)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_LogoutTwiceIsIdempotent(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)
	sessionID := f.sessionID(t)

	first := f.doLogout(t, f.domain, accessToken, false)
	if first.Code != http.StatusOK {
		t.Fatalf("first logout status = %d, want 200; body = %s", first.Code, first.Body.String())
	}

	// Revoking again directly (not through the now-blocklisted token,
	// which Authenticate itself would reject) exercises Revoke's own
	// idempotency, the same guarantee a client double-submitting the
	// logout request before the first response arrives relies on.
	if err := f.revoker.Revoke(context.Background(), sessionID, "logout"); err != nil {
		t.Errorf("second Revoke() error: %v, want idempotent success", err)
	}
}
