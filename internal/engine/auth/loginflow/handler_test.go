package loginflow

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/handoff"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
const testPassword = "correct horse battery staple"

// fixture is one login's worth of real, FK-satisfying rows: a tenant, an
// active user with a real Argon2id-hashed password and an admin role
// grant in that tenant — mirrors authtoken/authcheck's own test fixture
// shape.
type fixture struct {
	handler    *Handler
	tenantSlug string
	userID     string
	users      *user.Store
	mfaStore   *mfa.Store
	config     *tenantconfig.Store
	mfaTokens  *mfatoken.Codec
	tenantID   string
	conn       *sql.DB
	cache      *cache.Client
	email      string
	remoteIP   string
	// host resolves to the fixture's tenant; doLogin sends it unless a
	// test sets its own Host header.
	host string
}

// testPlatformDomain is the fixture's GOERP_PLATFORM_DOMAIN, so a
// handoff's host is {slug}.loginflow.test.
const testPlatformDomain = "loginflow.test"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockSigningKeyTable(t, conn)
	lockMFATokenSigningKeyTable(t, conn)

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
	mfaStore := mfa.NewStore(conn)
	if err := mfaStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
	}
	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	keySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}
	mfaTokenKeyStore := mfatoken.NewStore(conn, &secrets.EnvBackend{})
	if err := mfaTokenKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfatoken Bootstrap() error: %v", err)
	}
	mfaTokenKeySet, err := mfaTokenKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("mfatoken LoadOrGenerate() error: %v", err)
	}
	roleStore := role.NewStore(conn)

	slug := fmt.Sprintf("loginflowtest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Login Flow Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
	host := slug + "." + testPlatformDomain
	if _, err := tenantStore.CreateDomain(ctx, tt.ID, host, tenant.DomainSubdomain, true); err != nil {
		t.Fatalf("CreateDomain() error: %v", err)
	}

	email := slug + "@example.com"
	userID, err := userStore.FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })

	hash, err := argon2id.CreateHash(testPassword, password.ArgonParams)
	if err != nil {
		t.Fatalf("CreateHash() error: %v", err)
	}
	if _, err := conn.Exec(`UPDATE system.users SET status = 'active', password_hash = $2 WHERE id = $1`, userID, hash); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}

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

	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.user_mfa WHERE user_id = $1`, userID) })

	issuer := authtoken.NewIssuer(&keySet.Active, tenantStore, roleStore, sessionStore)
	mfaTokens := mfatoken.NewCodec(&mfaTokenKeySet.Active)
	configStore := tenantconfig.NewStore(conn)
	if err := configStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}
	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	// A per-fixture client IP keeps one test's attempts out of another's
	// per-IP window in the shared dev Redis.
	remoteIP := randomTestIP()
	t.Cleanup(func() {
		for _, key := range []string{"ratelimit:login:ip:" + remoteIP, "ratelimit:login:tenant:" + tt.ID, "ratelimit:login:tenant_alerted:" + tt.ID} {
			_ = cacheClient.Delete(context.Background(), key)
		}
	})

	auditStore := authaudit.NewStore(conn, tenantStore)
	if err := auditStore.Bootstrap(ctx); err != nil {
		t.Fatalf("authaudit Bootstrap() error: %v", err)
	}
	// Registered after the tenant's own cleanup, so it runs first:
	// auth_audit_log.tenant_id references system.tenants.
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.auth_audit_log WHERE tenant_id = $1`, tt.ID) })

	billingStore := billing.NewStore(conn)
	if err := billingStore.Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}
	resolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)
	handoffs := handoff.NewStore(cacheClient, resolver, testPlatformDomain)

	handler := NewHandler(userStore, tenantStore, roleStore, mfaStore, issuer, mfaTokens, password.NewPolicyStore(configStore), password.NewHasher(1024, time.Second), cacheClient, auditStore, resolver, handoffs)

	return &fixture{
		handler:    handler,
		tenantSlug: slug,
		userID:     userID,
		users:      userStore,
		mfaStore:   mfaStore,
		config:     configStore,
		mfaTokens:  mfaTokens,
		tenantID:   tt.ID,
		conn:       conn,
		cache:      cacheClient,
		email:      email,
		remoteIP:   remoteIP,
		host:       host,
	}
}

// lockSigningKeyTable mirrors authtoken.lockSigningKeyTable's own doc
// comment for why this is needed — serializes this package's tests
// against every other package's test touching the shared
// system.jwt_signing_keys table.
func lockSigningKeyTable(t *testing.T, pool *sql.DB) {
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

// lockMFATokenSigningKeyTable mirrors lockSigningKeyTable — serializes
// this package's tests against every other package's test touching the
// shared system.mfa_token_signing_keys table.
func lockMFATokenSigningKeyTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	key := db.AdvisoryLockKey("test.mfa_token_signing_keys_table")

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire dedicated connection for mfa-token-key lock: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatalf("acquire mfa-token-key advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		_ = conn.Close()
	})
}

// randomTestIP returns an address in 198.18.0.0/15, reserved for
// benchmarking, so it never collides with a real client.
func randomTestIP() string {
	n := rand.Uint32()
	return fmt.Sprintf("198.%d.%d.%d", 18+n>>16&1, n>>8&0xff, n&0xff)
}

// doLogin also clears the per-email window for the email it submits, so
// repeated runs against the shared dev Redis don't accumulate attempts.
func (f *fixture) doLogin(t *testing.T, body map[string]any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	if email, ok := body["email"].(string); ok {
		key := emailKey(strings.ToLower(email))
		t.Cleanup(func() { _ = f.cache.Delete(context.Background(), key) })
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(b))
	req.RemoteAddr = f.remoteIP + ":54321"
	req.Host = f.host
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestServeHTTP_SuccessNonBrowser_IssuesTokensInJSON(t *testing.T) {
	f := newFixture(t)

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, map[string]string{"X-Client-Type": "cli"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["access_token"] == "" || body["access_token"] == nil {
		t.Error("access_token missing or empty")
	}
	if body["refresh_token"] == "" || body["refresh_token"] == nil {
		t.Error("refresh_token missing or empty")
	}
	if body["device_id"] == "" || body["device_id"] == nil {
		t.Error("device_id missing or empty")
	}
	if body["expires_in"] != float64(900) {
		t.Errorf("expires_in = %v, want 900", body["expires_in"])
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("non-browser response set cookies, want none")
	}
}

func TestServeHTTP_SuccessWeb_SetsCookiesNotTokenBody(t *testing.T) {
	f := newFixture(t)

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if _, ok := body["access_token"]; ok {
		t.Error("web response body contains access_token, want cookie-only")
	}
	if body["expires_in"] != float64(900) {
		t.Errorf("expires_in = %v, want 900", body["expires_in"])
	}

	cookies := rec.Result().Cookies()
	names := map[string]*http.Cookie{}
	for _, c := range cookies {
		names[c.Name] = c
	}
	if _, ok := names["__Host-access_token"]; !ok {
		t.Error("missing __Host-access_token cookie")
	}
	if _, ok := names["refresh_token"]; !ok {
		t.Error("missing refresh_token cookie")
	}
	if _, ok := names["device_id"]; !ok {
		t.Error("missing device_id cookie for a freshly generated device id")
	}
	if c := names["__Host-access_token"]; c != nil {
		if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
			t.Errorf("__Host-access_token cookie flags = %+v, want HttpOnly+Secure+Strict+Path=/", c)
		}
	}
}

func TestServeHTTP_RememberControlsSessionPersistence(t *testing.T) {
	cases := []struct {
		name           string
		remember       bool
		headers        map[string]string
		wantPersistent bool
	}{
		{name: "web with remember", remember: true, wantPersistent: true},
		{name: "web without remember", remember: false, wantPersistent: false},
		{name: "non-browser without remember", remember: false, headers: map[string]string{"X-Client-Type": "cli"}, wantPersistent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)

			rec := f.doLogin(t, map[string]any{
				"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug, "remember": tc.remember,
			}, tc.headers)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
			}

			var persistent bool
			var ttl time.Duration
			if err := f.conn.QueryRow(
				`SELECT persistent, EXTRACT(EPOCH FROM expires_at - created_at)::bigint * 1000000000 FROM system.sessions WHERE user_id = $1`, f.userID,
			).Scan(&persistent, &ttl); err != nil {
				t.Fatalf("query session: %v", err)
			}
			if persistent != tc.wantPersistent {
				t.Errorf("session persistent = %v, want %v", persistent, tc.wantPersistent)
			}
			wantTTL := authtoken.NonPersistentRefreshTTL
			if tc.wantPersistent {
				wantTTL = authtoken.PersistentRefreshTTL
			}
			if diff := ttl - wantTTL; diff < -time.Minute || diff > time.Minute {
				t.Errorf("session lifetime = %v, want ~%v", ttl, wantTTL)
			}

			for _, c := range rec.Result().Cookies() {
				if c.Name != "refresh_token" {
					continue
				}
				if tc.wantPersistent && c.MaxAge != int(authtoken.PersistentRefreshTTL.Seconds()) {
					t.Errorf("refresh_token MaxAge = %d, want %d", c.MaxAge, int(authtoken.PersistentRefreshTTL.Seconds()))
				}
				if !tc.wantPersistent && c.MaxAge != 0 {
					t.Errorf("refresh_token MaxAge = %d, want 0 (browser-session cookie)", c.MaxAge)
				}
			}
		})
	}
}

func TestServeHTTP_MFAEnrolled_TokenCarriesRemember(t *testing.T) {
	f := newFixture(t)
	if _, err := f.mfaStore.Insert(context.Background(), f.userID, mfa.CredentialTOTP, []byte("x"), nil); err != nil {
		t.Fatalf("Insert() mfa credential error: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug, "remember": true,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	token, _ := decodeBody(t, rec)["mfa_token"].(string)
	claims, err := f.handler.mfaTokens.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if !claims.Remember {
		t.Error("mfa_token Remember = false, want true")
	}
}

func TestServeHTTP_WrongPassword_ReturnsInvalidCredentialsAndIncrementsCounter(t *testing.T) {
	f := newFixture(t)

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": "wrong-password", "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "invalid_credentials" {
		t.Errorf("error.code = %v, want invalid_credentials", errObj["code"])
	}

	got, err := f.users.GetByID(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.FailedLoginCount != 1 {
		t.Errorf("FailedLoginCount = %d, want 1", got.FailedLoginCount)
	}
}

func TestServeHTTP_UnknownEmail_ReturnsInvalidCredentials(t *testing.T) {
	f := newFixture(t)

	rec := f.doLogin(t, map[string]any{
		"email": "no-such-user@example.com", "password": "whatever", "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "invalid_credentials" {
		t.Errorf("error.code = %v, want invalid_credentials", errObj["code"])
	}
}

func TestServeHTTP_InvitedStatus_NoPasswordSetReturnsInvalidCredentials(t *testing.T) {
	f := newFixture(t)
	email := fmt.Sprintf("invited%d@example.com", time.Now().UnixNano())
	if _, err := f.users.FindOrCreateInvited(context.Background(), email); err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.users WHERE email = $1`, email) })

	rec := f.doLogin(t, map[string]any{
		"email": email, "password": "anything", "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_SuspendedStatus_ReturnsInvalidCredentials(t *testing.T) {
	f := newFixture(t)
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'suspended' WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("suspend fixture user: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_PendingVerificationStatus_ReturnsEmailVerificationRequired(t *testing.T) {
	f := newFixture(t)
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'pending_verification' WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("set pending_verification: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "email_verification_required" {
		t.Errorf("error.code = %v, want email_verification_required", errObj["code"])
	}
}

func TestServeHTTP_NoTenantMembership_ReturnsInvalidCredentials(t *testing.T) {
	f := newFixture(t)
	tenantStore := tenant.NewStore(f.conn)
	otherSlug := fmt.Sprintf("otherloginflow%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(context.Background(), otherSlug, "Other Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": otherSlug,
	}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", rec.Code, rec.Body.String())
	}
}

// testLockThreshold mirrors user.failedLoginLockThreshold's value (10) —
// unexported in that package, so this test duplicates the constant
// rather than the mechanism.
const testLockThreshold = 10

func TestServeHTTP_LockedAccount_ReturnsInvalidCredentials(t *testing.T) {
	f := newFixture(t)
	for range testLockThreshold {
		if err := f.users.IncrementFailedLogins(context.Background(), f.userID); err != nil {
			t.Fatalf("IncrementFailedLogins() error: %v", err)
		}
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401 even with the correct password, since the account is locked", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_MFAEnrolled_ReturnsRequiredWithoutSession(t *testing.T) {
	f := newFixture(t)
	if _, err := f.mfaStore.Insert(context.Background(), f.userID, mfa.CredentialTOTP, []byte("x"), nil); err != nil {
		t.Fatalf("Insert() mfa credential error: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["mfa_required"] != true {
		t.Errorf("mfa_required = %v, want true", body["mfa_required"])
	}
	if body["mfa_token"] == "" || body["mfa_token"] == nil {
		t.Error("mfa_token missing or empty")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("MFA-required response set cookies, want none — no session issued yet")
	}

	var sessionCount int
	if err := f.conn.QueryRow(`SELECT count(*) FROM system.sessions WHERE user_id = $1`, f.userID).Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Errorf("session count = %d, want 0 — MFA gating must not issue a session", sessionCount)
	}
}

func TestServeHTTP_OutdatedParams_ReHashesStoredHash(t *testing.T) {
	f := newFixture(t)
	oldParams := &argon2id.Params{Memory: 32 * 1024, Iterations: 1, Parallelism: 2, SaltLength: 16, KeyLength: 32}
	oldHash, err := argon2id.CreateHash(testPassword, oldParams)
	if err != nil {
		t.Fatalf("CreateHash() error: %v", err)
	}
	if _, err := f.conn.Exec(`UPDATE system.users SET password_hash = $2 WHERE id = $1`, f.userID, oldHash); err != nil {
		t.Fatalf("set old-params hash: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}

	var storedHash string
	if err := f.conn.QueryRow(`SELECT password_hash FROM system.users WHERE id = $1`, f.userID).Scan(&storedHash); err != nil {
		t.Fatalf("query password_hash: %v", err)
	}
	if storedHash == oldHash {
		t.Error("password_hash unchanged after login with outdated params, want a re-hash")
	}
	match, params, err := argon2id.CheckHash(testPassword, storedHash)
	if err != nil || !match {
		t.Fatalf("re-hashed hash doesn't verify: match=%v err=%v", match, err)
	}
	if *params != *password.ArgonParams {
		t.Errorf("re-hashed params = %+v, want current argonParams", params)
	}
}

func TestServeHTTP_SuccessResetsFailedLoginCounter(t *testing.T) {
	f := newFixture(t)
	if err := f.users.IncrementFailedLogins(context.Background(), f.userID); err != nil {
		t.Fatalf("IncrementFailedLogins() error: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}

	got, err := f.users.GetByID(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.FailedLoginCount != 0 {
		t.Errorf("FailedLoginCount = %d, want 0 after a successful login", got.FailedLoginCount)
	}
}

func fixtureEmail(f *fixture) string {
	return f.tenantSlug + "@example.com"
}

func TestServeHTTP_PolicyVersionBehind_RecommendsUpdateWithoutBlocking(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyMinLength, "16"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, map[string]string{"X-Client-Type": "cli"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["password_update_recommended"] != true {
		t.Errorf("password_update_recommended = %v, want true", body["password_update_recommended"])
	}
	if body["access_token"] == nil {
		t.Error("access_token missing — the nudge must not block login")
	}
}

func TestServeHTTP_PolicyVersionCurrent_NoRecommendation(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyMinLength, "16"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}
	if _, err := f.conn.Exec(`UPDATE system.users SET password_set_at_policy_tenant_id = $2, password_set_at_policy_version = 1 WHERE id = $1`, f.userID, f.tenantID); err != nil {
		t.Fatalf("set user policy version: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if _, ok := decodeBody(t, rec)["password_update_recommended"]; ok {
		t.Error("password_update_recommended present, want it omitted when the user is current")
	}
}

func TestServeHTTP_MFAEnrolledPolicyVersionBehind_TokenCarriesRecommendation(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyMinLength, "16"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}
	if _, err := f.mfaStore.Insert(t.Context(), f.userID, mfa.CredentialTOTP, []byte("x"), nil); err != nil {
		t.Fatalf("Insert() mfa credential error: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)

	token, _ := decodeBody(t, rec)["mfa_token"].(string)
	claims, err := f.mfaTokens.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if !claims.PasswordUpdateRecommended {
		t.Error("mfa_token PasswordUpdateRecommended = false, want true")
	}
}

func TestServeHTTP_PasswordValidatedInAnotherTenant_RecommendsUpdate(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyRequireSymbol, "true"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}
	// A higher version recorded against a different tenant must not count.
	if _, err := f.conn.Exec(`UPDATE system.users SET password_set_at_policy_tenant_id = gen_random_uuid(), password_set_at_policy_version = 5 WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("set user policy version: %v", err)
	}

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, nil)

	if rec.Code != http.StatusOK || decodeBody(t, rec)["password_update_recommended"] != true {
		t.Errorf("status = %d, body = %s, want 200 with password_update_recommended", rec.Code, rec.Body.String())
	}
}

// saturatedHasher has its only slot held for the test's lifetime, so every
// Acquire times out.
func saturatedHasher(t *testing.T) *password.Hasher {
	t.Helper()
	h := password.NewHasher(64, 10*time.Millisecond)
	slot, err := h.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	t.Cleanup(slot.Release)
	return h
}

func assertOverloaded(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "overloaded" {
		t.Fatalf("status = %d, body = %s, want 503 overloaded", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want \"1\"", got)
	}
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	errObj, _ := decodeBody(t, rec)["error"].(map[string]any)
	code, _ := errObj["code"].(string)
	return code
}

func TestServeHTTP_Overloaded_Returns503BeforeLookingUpTheUser(t *testing.T) {
	f := newFixture(t)
	f.handler.hasher = saturatedHasher(t)

	for _, email := range []string{fixtureEmail(f), "no-such-user@example.com"} {
		rec := f.doLogin(t, map[string]any{
			"email": email, "password": "wrong-password", "tenant": f.tenantSlug,
		}, nil)
		assertOverloaded(t, rec)
	}

	got, err := f.users.GetByID(t.Context(), f.userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.FailedLoginCount != 0 {
		t.Errorf("FailedLoginCount = %d, want 0 — an overloaded login never checked the password", got.FailedLoginCount)
	}
}
