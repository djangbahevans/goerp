package authcheck

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/session"
	"github.com/djangbahevans/goerp/internal/engine/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/golang-jwt/jwt/v5"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
const localRedisAddr = "localhost:6379"

const testPermission = "widgets.read"

// fixture is one login's worth of real, FK-satisfying rows, plus an
// Issuer to mint real tokens and a Checker to validate them against.
// Cleaned up by exact row id — system.tenants/system.users/system.sessions
// are real shared tables other packages' tests race against concurrently.
type fixture struct {
	issuer      *authtoken.Issuer
	checker     *Checker
	tenantID    string
	tenantSlug  string
	userID      string
	permissions *permission.PermissionRegistry
	conn        *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cacheClient, err := cache.New(ctx, cache.Config{Addr: localRedisAddr, DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at %s (start compose.dev.yml): %v", localRedisAddr, err)
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
	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	keySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}

	slug := fmt.Sprintf("authchecktest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Auth Check Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	userID, err := userStore.FindOrCreateInvited(ctx, slug+"@example.com")
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	if _, err := conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })

	schema := tenantschema.Name(slug)
	if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)) })

	roleStore := role.NewStore(conn)
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
	if _, err := conn.Exec(fmt.Sprintf("INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)", schema), roleID, testPermission); err != nil {
		t.Fatalf("grant permission: %v", err)
	}

	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })

	registry := permission.NewPermissionRegistry()
	registry.Register("widgets", []manifest.Permission{{Name: testPermission}})

	return &fixture{
		issuer:      authtoken.NewIssuer(&keySet.Active, tenantStore, roleStore, sessionStore),
		checker:     NewChecker(&keySet.Active, sessionrevoke.NewRevoker(sessionStore, cacheClient), userStore, roleStore),
		tenantID:    tt.ID,
		tenantSlug:  slug,
		userID:      userID,
		permissions: registry,
		conn:        conn,
	}
}

func (f *fixture) issueToken(t *testing.T, deviceID string) string {
	t.Helper()
	if deviceID == "" {
		deviceID = "11111111-1111-1111-1111-111111111111"
	}
	tokens, err := f.issuer.Issue(context.Background(), authtoken.LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   deviceID,
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func TestAuthenticate_ValidTokenProducesAuthenticatedContext(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	authCtx, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, f.permissions, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	if !authCtx.IsAuthenticated {
		t.Fatal("IsAuthenticated = false, want true")
	}
	if authCtx.UserID != f.userID {
		t.Errorf("UserID = %q, want %q", authCtx.UserID, f.userID)
	}
	if authCtx.TenantID != f.tenantID {
		t.Errorf("TenantID = %q, want %q", authCtx.TenantID, f.tenantID)
	}
	if authCtx.SessionID == "" {
		t.Error("SessionID is empty")
	}
	if len(authCtx.RolesLive) != 1 || authCtx.RolesLive[0] != "admin" {
		t.Errorf("RolesLive = %v, want [admin]", authCtx.RolesLive)
	}
	idx, ok := f.permissions.Index(testPermission)
	if !ok {
		t.Fatalf("test setup: %s not registered", testPermission)
	}
	if !authCtx.PermissionSet.Has(idx) {
		t.Error("PermissionSet does not have the granted permission")
	}
	if authCtx.MFAVerified {
		t.Error("MFAVerified = true, want false — no MFA support yet")
	}
}

func TestAuthenticate_EmptyTokenIsAnonymousNotError(t *testing.T) {
	f := newFixture(t)

	authCtx, err := f.checker.Authenticate(context.Background(), "", f.tenantID, f.tenantSlug, f.permissions, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v, want nil for empty token", err)
	}
	if authCtx.IsAuthenticated {
		t.Error("IsAuthenticated = true for an empty token, want false (Anonymous)")
	}
}

func TestAuthenticate_MalformedTokenIsRejected(t *testing.T) {
	f := newFixture(t)

	_, err := f.checker.Authenticate(context.Background(), "not-a-jwt", f.tenantID, f.tenantSlug, f.permissions, nil)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Authenticate() error = %v, want ErrInvalidToken", err)
	}
}

func TestAuthenticate_ExpiredTokenIsRejected(t *testing.T) {
	f := newFixture(t)
	token := f.signRawClaims(t, authtoken.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "goerp",
			Subject:   f.userID,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			ID:        "expired-jti",
		},
		SessionID: "22222222-2222-2222-2222-222222222222",
		TenantID:  f.tenantID,
		Scope:     []string{"api"},
		AMR:       []string{"pwd"},
	})

	_, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, f.permissions, nil)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Authenticate() error = %v, want ErrInvalidToken (expired)", err)
	}
}

func TestAuthenticate_WrongSignatureIsRejected(t *testing.T) {
	f := newFixture(t)

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	claims := authtoken.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "goerp",
			Subject:   f.userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			ID:        "wrong-sig-jti",
		},
		SessionID: "33333333-3333-3333-3333-333333333333",
		TenantID:  f.tenantID,
		Scope:     []string{"api"},
		AMR:       []string{"pwd"},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = f.checker.signingKey.KID // claims the real kid, but signs with a different key
	signed, err := tok.SignedString(otherKey)
	if err != nil {
		t.Fatalf("sign with other key: %v", err)
	}

	_, err = f.checker.Authenticate(context.Background(), signed, f.tenantID, f.tenantSlug, f.permissions, nil)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Authenticate() error = %v, want ErrInvalidToken (wrong signature)", err)
	}
}

func TestAuthenticate_RevokedSessionIsRejected(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	// Parse just to grab the sid, then revoke that session directly.
	claims := &authtoken.Claims{}
	_, err := jwt.ParseWithClaims(token, claims, f.checker.keyFunc)
	if err != nil {
		t.Fatalf("parse issued token: %v", err)
	}
	if err := f.checker.revoker.Revoke(context.Background(), claims.SessionID, "test"); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	_, err = f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, f.permissions, nil)
	if !errors.Is(err, ErrSessionRevoked) {
		t.Errorf("Authenticate() error = %v, want ErrSessionRevoked", err)
	}
}

func TestAuthenticate_TenantMismatchIsRejected(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	_, err := f.checker.Authenticate(context.Background(), token, "00000000-0000-0000-0000-000000000000", f.tenantSlug, f.permissions, nil)
	if !errors.Is(err, ErrTenantMismatch) {
		t.Errorf("Authenticate() error = %v, want ErrTenantMismatch", err)
	}
}

func TestAuthenticate_SuspendedUserIsRejected(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'suspended' WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("suspend fixture user: %v", err)
	}

	_, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, f.permissions, nil)
	if !errors.Is(err, ErrUserNotActive) {
		t.Errorf("Authenticate() error = %v, want ErrUserNotActive", err)
	}
}

func TestAuthenticate_NonMemberIsRejected(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	schema := tenantschema.Name(f.tenantSlug)
	if _, err := f.conn.Exec(fmt.Sprintf("DELETE FROM %s.user_roles WHERE user_id = $1", schema), f.userID); err != nil {
		t.Fatalf("remove membership: %v", err)
	}

	_, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, f.permissions, nil)
	if !errors.Is(err, ErrNotTenantMember) {
		t.Errorf("Authenticate() error = %v, want ErrNotTenantMember", err)
	}
}

func TestAuthenticate_MissingRequiredPermissionIsRejected(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	_, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, f.permissions, []string{"widgets.delete"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("Authenticate() error = %v, want ErrPermissionDenied", err)
	}
}

func TestAuthenticate_GrantedRequiredPermissionSucceeds(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	authCtx, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, f.permissions, []string{testPermission})
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	if !authCtx.IsAuthenticated {
		t.Error("IsAuthenticated = false, want true")
	}
}

func TestExtractToken_HeaderTakesPrecedenceOverCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer header-token")
	r.AddCookie(&http.Cookie{Name: accessTokenCookieName, Value: "cookie-token"})

	if got := ExtractToken(r); got != "header-token" {
		t.Errorf("ExtractToken() = %q, want header-token", got)
	}
}

func TestExtractToken_FallsBackToCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: accessTokenCookieName, Value: "cookie-token"})

	if got := ExtractToken(r); got != "cookie-token" {
		t.Errorf("ExtractToken() = %q, want cookie-token", got)
	}
}

func TestExtractToken_EmptyWhenNeitherPresent(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	if got := ExtractToken(r); got != "" {
		t.Errorf("ExtractToken() = %q, want empty", got)
	}
}

// signRawClaims signs claims with the fixture's real signing key, bypassing
// authtoken.Issuer — needed for test cases (expired, wrong session, etc.)
// Issue's own API doesn't let a caller construct.
func (f *fixture) signRawClaims(t *testing.T, claims authtoken.Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = f.checker.signingKey.KID
	signed, err := tok.SignedString(f.checker.signingKey.Private)
	if err != nil {
		t.Fatalf("sign claims: %v", err)
	}
	return signed
}
