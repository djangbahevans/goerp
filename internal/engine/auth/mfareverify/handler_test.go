package mfareverify

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pquernatotp "github.com/pquerna/otp/totp"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/lockout"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// fixture is one authenticated session's worth of real, FK-satisfying
// rows — a tenant with a resolvable subdomain, an active user with an
// admin role grant, and both an Issuer/Checker pair (to mint and validate
// real tokens) and a Resolver (to resolve the fixture's own Host header)
// — mirroring authcheck's and tenantresolve's own test fixture shapes,
// combined, since this handler is the first real caller of both together.
type fixture struct {
	handler    *Handler
	issuer     *authtoken.Issuer
	rowKeys    *rowcrypt.RowKeySet
	domain     string
	tenantID   string
	tenantSlug string
	userID     string
	conn       *sql.DB
	cache      *cache.Client
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockSharedKeyTables(t, conn)

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
	mfaStore := mfa.NewStore(conn)
	if err := mfaStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
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

	rowCryptStore := rowcrypt.NewStore(conn, &secrets.EnvBackend{})
	if err := rowCryptStore.Bootstrap(ctx); err != nil {
		t.Fatalf("rowcrypt Bootstrap() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.row_encryption_keys`) })
	rowKeys, err := rowCryptStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("rowcrypt LoadOrGenerate() error: %v", err)
	}

	tenantResolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)
	issuer := authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore)
	roleCache := permcache.NewRoleCache(cacheClient)
	roleMap := permcache.NewRolePermissionMap()
	registry := permission.NewPermissionRegistry()
	authChecker := authcheck.NewChecker(&signingKeySet.Active, sessionrevoke.NewRevoker(sessionStore, cacheClient), userStore, roleStore, roleCache, roleMap, apiKeys, false)
	totpService := totp.NewService(mfaStore, rowKeys, cacheClient)
	recoveryService := recoverycode.NewService(mfaStore)
	lockoutCounter := lockout.NewCounter(cacheClient)
	handler := NewHandler(tenantResolver, authChecker, sessionStore, issuer, totpService, recoveryService, lockoutCounter)

	slug := fmt.Sprintf("mfareverifytest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "MFA Reverify Test Co")
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
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.user_mfa WHERE user_id = $1`, userID) })
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

	if err := roleMap.RebuildAll(ctx, tenantStore, roleStore, registry); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}

	return &fixture{
		handler:    handler,
		issuer:     issuer,
		rowKeys:    rowKeys,
		domain:     domain,
		tenantID:   tt.ID,
		tenantSlug: slug,
		userID:     userID,
		conn:       conn,
		cache:      cacheClient,
	}
}

// lockSharedKeyTables mirrors mfaverify's own lock helper — serializes
// this package's tests against every other package's test touching the
// same shared signing-key/row-encryption-key tables.
func lockSharedKeyTables(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{"test.jwt_signing_keys_table", "test.row_encryption_keys_table"} {
		key := db.AdvisoryLockKey(name)
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire dedicated connection for %s lock: %v", name, err)
		}
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
			t.Fatalf("acquire %s advisory lock: %v", name, err)
		}
		t.Cleanup(func() {
			_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
			_ = conn.Close()
		})
	}
}

// issueAccessToken mints a real, currently-valid access token for the
// fixture's user — the "already-Authenticated session" reverify requires.
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

func (f *fixture) enrollTOTP(t *testing.T) (code string) {
	t.Helper()
	svc := totp.NewService(mfa.NewStore(f.conn), f.rowKeys, f.cache)
	_, cred, err := svc.Enroll(context.Background(), f.userID, "user@example.com", nil)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	var ciphertext []byte
	if err := f.conn.QueryRowContext(context.Background(),
		"SELECT credential FROM system.user_mfa WHERE id = $1", cred.ID,
	).Scan(&ciphertext); err != nil {
		t.Fatalf("query stored credential: %v", err)
	}
	secret, err := f.rowKeys.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decrypt stored credential: %v", err)
	}
	code, err = pquernatotp.GenerateCode(string(secret), time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error: %v", err)
	}
	return code
}

func (f *fixture) doReverify(t *testing.T, accessToken string, body map[string]any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/mfa/reverify", bytes.NewReader(b))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_ValidTOTPCodeRefreshesAssuranceAndReissuesAccessToken(t *testing.T) {
	f := newFixture(t)
	code := f.enrollTOTP(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doReverify(t, accessToken, map[string]any{
		"type": "totp",
		"code": code,
	}, map[string]string{"X-Client-Type": "cli"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	newAccessToken, _ := resp["access_token"].(string)
	if newAccessToken == "" {
		t.Fatal("access_token missing or empty")
	}
	if newAccessToken == accessToken {
		t.Error("access_token unchanged, want a freshly reissued token")
	}

	var mfaMethod string
	var mfaVerifiedAt sql.NullTime
	if err := f.conn.QueryRowContext(context.Background(),
		`SELECT mfa_method, mfa_verified_at FROM system.sessions WHERE user_id = $1`, f.userID,
	).Scan(&mfaMethod, &mfaVerifiedAt); err != nil {
		t.Fatalf("query session row: %v", err)
	}
	if mfaMethod != "totp" {
		t.Errorf("sessions.mfa_method = %q, want totp", mfaMethod)
	}
	if !mfaVerifiedAt.Valid {
		t.Error("sessions.mfa_verified_at is NULL, want set")
	}
}

func TestServeHTTP_NoAccessTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doReverify(t, "", map[string]any{"type": "totp", "code": "123456"}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_MalformedAccessTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doReverify(t, "not-a-real-token", map[string]any{"type": "totp", "code": "123456"}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_WrongCodeRejected(t *testing.T) {
	f := newFixture(t)
	f.enrollTOTP(t)
	accessToken := f.issueAccessToken(t)

	rec := f.doReverify(t, accessToken, map[string]any{"type": "totp", "code": "000000"}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_UnresolvableHostRejected(t *testing.T) {
	f := newFixture(t)
	accessToken := f.issueAccessToken(t)

	b, _ := json.Marshal(map[string]any{"type": "totp", "code": "123456"})
	req := httptest.NewRequest(http.MethodPost, "/auth/mfa/reverify", bytes.NewReader(b))
	req.Host = "no-such-tenant.goerp.test"
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_LocksOutAfterFiveFailures(t *testing.T) {
	f := newFixture(t)
	f.enrollTOTP(t)
	accessToken := f.issueAccessToken(t)

	for attempt := range lockout.MaxAttempts {
		rec := f.doReverify(t, accessToken, map[string]any{"type": "totp", "code": "000000"}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt+1, rec.Code)
		}
	}

	code := f.enrollTOTP(t)
	rec := f.doReverify(t, accessToken, map[string]any{"type": "totp", "code": code}, nil)
	if rec.Code != http.StatusLocked {
		t.Errorf("status after %d failures = %d, want 423", lockout.MaxAttempts, rec.Code)
	}
}

func TestServeHTTP_ValidRecoveryCodeCompletesReverify(t *testing.T) {
	f := newFixture(t)
	svc := recoverycode.NewService(mfa.NewStore(f.conn))
	codes, err := svc.Enroll(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}
	accessToken := f.issueAccessToken(t)

	rec := f.doReverify(t, accessToken, map[string]any{
		"type": "recovery_code",
		"code": codes[0],
	}, map[string]string{"X-Client-Type": "cli"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}
