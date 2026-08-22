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

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/golang-jwt/jwt/v5"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
const localRedisAddr = "localhost:6379"

const testPermission = "widgets.read"
const testPermission2 = "widgets.write" // granted to the fixture's admin role too, for API-key scope-restriction tests

// fixture is one login's worth of real, FK-satisfying rows, plus an
// Issuer to mint real tokens and a Checker to validate them against.
// Cleaned up by exact row id — system.tenants/system.users/system.sessions
// are real shared tables other packages' tests race against concurrently.
type fixture struct {
	issuer       *authtoken.Issuer
	checker      *Checker
	tenantID     string
	tenantSlug   string
	userID       string
	permissions  *permission.PermissionRegistry
	conn         *sql.DB
	roleCache    *permcache.RoleCache
	roleMap      *permcache.RolePermissionMap
	apiKeys      *apikey.Store
	mfaTokens    *mfatoken.Codec
	mfaTokenKey  *mfatoken.Key
	mfaCreds     *mfa.Store
	tenantConfig *tenantconfig.Store
}

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
	mfaTokenKeyStore := mfatoken.NewStore(conn, &secrets.EnvBackend{})
	if err := mfaTokenKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfatoken Bootstrap() error: %v", err)
	}
	mfaTokenKeySet, err := mfaTokenKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("mfatoken LoadOrGenerate() error: %v", err)
	}
	mfaCreds := mfa.NewStore(conn)
	if err := mfaCreds.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
	}
	tenantConfig := tenantconfig.NewStore(conn)
	if err := tenantConfig.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("authchecktest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Auth Check Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
	// RolePermissionMap.RebuildAll only resolves roles for active tenants.
	if _, err := tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate fixture tenant: %v", err)
	}

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
	if _, err := conn.Exec(fmt.Sprintf("INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)", schema), roleID, testPermission2); err != nil {
		t.Fatalf("grant permission: %v", err)
	}

	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.user_mfa WHERE user_id = $1`, userID) })

	registry := permission.NewPermissionRegistry()
	registry.Register("widgets", []manifest.Permission{{Name: testPermission}, {Name: testPermission2}})

	roleMap := permcache.NewRolePermissionMap()
	if err := roleMap.RebuildAll(ctx, tenantStore, roleStore, registry); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}
	roleCache := permcache.NewRoleCache(cacheClient)

	apiKeys := apikey.NewStore(conn)
	if err := apiKeys.Bootstrap(ctx); err != nil {
		t.Fatalf("apikey Bootstrap() error: %v", err)
	}

	mfaTokens := mfatoken.NewCodec(&mfaTokenKeySet.Active)
	mfaPolicies := enforce.NewStore(tenantConfig)

	return &fixture{
		issuer:       authtoken.NewIssuer(&keySet.Active, tenantStore, roleStore, sessionStore),
		checker:      NewChecker(&keySet.Active, sessionrevoke.NewRevoker(sessionStore, cacheClient), userStore, roleStore, roleCache, roleMap, apiKeys, true, mfaTokens, mfaCreds, mfaPolicies),
		tenantID:     tt.ID,
		tenantSlug:   slug,
		userID:       userID,
		permissions:  registry,
		conn:         conn,
		roleCache:    roleCache,
		roleMap:      roleMap,
		apiKeys:      apiKeys,
		mfaTokens:    mfaTokens,
		mfaTokenKey:  &mfaTokenKeySet.Active,
		mfaCreds:     mfaCreds,
		tenantConfig: tenantConfig,
	}
}

// lockSigningKeyTable takes a session-scoped Postgres advisory lock
// (pg_advisory_lock, explicitly released at test cleanup) — a key
// distinct from the one LoadOrGenerate itself locks internally
// (db.WithAdvisoryLock's transaction-scoped pg_advisory_xact_lock), since
// holding that same key here would deadlock this test's own later
// LoadOrGenerate call against itself (a different pooled connection
// blocking on a lock this test's own session already holds). This
// serializes the test against every other package's test touching the
// shared system.jwt_signing_keys table instead: signingkey, authcheck,
// and authtoken tests all exercise its single-active-row constraint
// against the same real compose.dev.yml Postgres instance; without this,
// one package's in-flight active row is visible mid-test to another
// package's concurrently running test, whose own (different,
// process-local) secrets backend has no way to load that row's private
// key material — "parse private key material ...: no PEM block found".
// Safe here specifically because localPostgresDSN bypasses PgBouncer —
// a session-scoped lock isn't safe under PgBouncer's transaction pooling
// (see db.WithAdvisoryLock's doc comment for why production code uses a
// transaction-scoped lock instead).
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
		// sql.Conn.Close returns the connection to the pool for reuse
		// rather than necessarily terminating the physical session, so it
		// does not by itself release a session-scoped advisory lock —
		// unlock explicitly first, or the next test wanting this lock
		// hangs forever waiting on one nothing will ever release.
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		_ = conn.Close()
	})
}

// lockMFATokenSigningKeyTable mirrors lockSigningKeyTable — serializes
// this package's tests against every other package's test touching the
// shared system.mfa_token_signing_keys table (mfatoken, mfaverify,
// loginflow all exercise its own single-active-row constraint).
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

	authCtx, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
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
		t.Error("MFAVerified = true, want false for an ordinary password-only login")
	}
}

func TestAuthenticate_MFAVerifiedSessionPopulatesMFAFields(t *testing.T) {
	f := newFixture(t)
	verifiedAt := time.Now().Add(-time.Minute)
	tokens, err := f.issuer.Issue(context.Background(), authtoken.LoginParams{
		UserID:        f.userID,
		TenantSlug:    f.tenantSlug,
		DeviceID:      "11111111-1111-1111-1111-111111111111",
		MFAMethod:     "totp",
		MFAVerifiedAt: &verifiedAt,
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	authCtx, err := f.checker.Authenticate(context.Background(), tokens.AccessToken, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	if !authCtx.MFAVerified {
		t.Error("MFAVerified = false, want true for a token minted with MFAMethod set")
	}
	if len(authCtx.AMR) != 2 || authCtx.AMR[0] != "pwd" || authCtx.AMR[1] != "totp" {
		t.Errorf("AMR = %v, want [pwd totp]", authCtx.AMR)
	}
	if authCtx.MFAVerifiedAt == nil {
		t.Fatal("MFAVerifiedAt is nil, want the verification timestamp")
	}
	if authCtx.MFAVerifiedAt.Unix() != verifiedAt.Unix() {
		t.Errorf("MFAVerifiedAt = %v, want %v", authCtx.MFAVerifiedAt, verifiedAt)
	}
}

func TestAuthenticate_PermissionSetPopulatesRoleCacheOnMiss(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")
	ctx := context.Background()

	if _, err := f.checker.Authenticate(ctx, token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil); err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}

	roleIDs, found := f.roleCache.Get(ctx, f.tenantID, f.userID)
	if !found {
		t.Fatal("RoleCache.Get() found = false after Authenticate, want true")
	}
	if len(roleIDs) != 1 {
		t.Errorf("cached roleIDs = %v, want exactly the admin role", roleIDs)
	}
}

func TestAuthenticate_UsesCachedRolesOnSecondCall(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")
	ctx := context.Background()

	// A cache entry naming a role RolePermissionMap doesn't resolve — a
	// live DB read would still find the real (correctly granted) admin
	// role, so if Authenticate's result reflects this instead, it proves
	// the cache was actually consulted rather than bypassed.
	f.roleCache.Set(ctx, f.tenantID, f.userID, []string{"00000000-0000-0000-0000-000000000000"})

	authCtx, err := f.checker.Authenticate(ctx, token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	idx, ok := f.permissions.Index(testPermission)
	if !ok {
		t.Fatalf("test setup: %s not registered", testPermission)
	}
	if authCtx.PermissionSet.Has(idx) {
		t.Error("PermissionSet has the granted permission, want it absent — cached (bogus) role should have taken precedence over the live DB grant")
	}
}

func TestAuthenticate_StaleMarkerBypassesRoleCache(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")
	ctx := context.Background()

	claims := &authtoken.Claims{}
	if _, err := jwt.ParseWithClaims(token, claims, f.checker.keyFunc); err != nil {
		t.Fatalf("parse issued token: %v", err)
	}

	// Same bogus cache entry as TestAuthenticate_UsesCachedRolesOnSecondCall,
	// but this time the session is marked stale first — Authenticate should
	// bypass the cache and read the real (correctly granted) role from
	// Postgres instead.
	f.roleCache.Set(ctx, f.tenantID, f.userID, []string{"00000000-0000-0000-0000-000000000000"})
	if err := f.checker.revoker.MarkRolesStale(ctx, claims.SessionID); err != nil {
		t.Fatalf("MarkRolesStale() error: %v", err)
	}

	authCtx, err := f.checker.Authenticate(ctx, token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	idx, ok := f.permissions.Index(testPermission)
	if !ok {
		t.Fatalf("test setup: %s not registered", testPermission)
	}
	if !authCtx.PermissionSet.Has(idx) {
		t.Error("PermissionSet does not have the granted permission — stale marker should have forced a live DB read past the bogus cache entry")
	}
}

func TestAuthenticate_EmptyTokenIsAnonymousNotError(t *testing.T) {
	f := newFixture(t)

	authCtx, err := f.checker.Authenticate(context.Background(), "", f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v, want nil for empty token", err)
	}
	if authCtx.IsAuthenticated {
		t.Error("IsAuthenticated = true for an empty token, want false (Anonymous)")
	}
}

func TestAuthenticate_MalformedTokenIsRejected(t *testing.T) {
	f := newFixture(t)

	_, err := f.checker.Authenticate(context.Background(), "not-a-jwt", f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
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

	_, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
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

	_, err = f.checker.Authenticate(context.Background(), signed, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
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

	_, err = f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if !errors.Is(err, ErrSessionRevoked) {
		t.Errorf("Authenticate() error = %v, want ErrSessionRevoked", err)
	}
}

func TestAuthenticate_TenantMismatchIsRejected(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	_, err := f.checker.Authenticate(context.Background(), token, "00000000-0000-0000-0000-000000000000", f.tenantSlug, "203.0.113.1", f.permissions, nil)
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

	_, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
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

	_, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if !errors.Is(err, ErrNotTenantMember) {
		t.Errorf("Authenticate() error = %v, want ErrNotTenantMember", err)
	}
}

func TestAuthenticate_MissingRequiredPermissionIsRejected(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	_, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, []string{"widgets.delete"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("Authenticate() error = %v, want ErrPermissionDenied", err)
	}
}

func TestAuthenticate_GrantedRequiredPermissionSucceeds(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t, "")

	authCtx, err := f.checker.Authenticate(context.Background(), token, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, []string{testPermission})
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

func TestAuthenticate_APIKeyValidKeySucceeds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fullKey, key, err := f.apiKeys.IssueKey(ctx, f.tenantID, &f.userID, "Test Key", []string{testPermission, testPermission2}, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	authCtx, err := f.checker.Authenticate(ctx, fullKey, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	if !authCtx.IsAuthenticated {
		t.Fatal("IsAuthenticated = false, want true")
	}
	if authCtx.AuthMethod != "api_key" {
		t.Errorf("AuthMethod = %q, want %q", authCtx.AuthMethod, "api_key")
	}
	if authCtx.UserID != f.userID {
		t.Errorf("UserID = %q, want %q", authCtx.UserID, f.userID)
	}
	if authCtx.APIKey == nil || authCtx.APIKey.ID != key.ID {
		t.Errorf("APIKey = %v, want the issued key %q", authCtx.APIKey, key.ID)
	}
	idx, _ := f.permissions.Index(testPermission)
	if !authCtx.PermissionSet.Has(idx) {
		t.Error("PermissionSet does not have the granted permission")
	}
}

func TestAuthenticate_APIKeyServiceKeyUsesOnlyScopes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fullKey, _, err := f.apiKeys.IssueKey(ctx, f.tenantID, nil, "Service Key", []string{testPermission}, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	authCtx, err := f.checker.Authenticate(ctx, fullKey, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	if authCtx.UserID != "" {
		t.Errorf("UserID = %q, want empty for a service key", authCtx.UserID)
	}
	idx1, _ := f.permissions.Index(testPermission)
	if !authCtx.PermissionSet.Has(idx1) {
		t.Error("PermissionSet does not have the key's own scope")
	}
	idx2, _ := f.permissions.Index(testPermission2)
	if authCtx.PermissionSet.Has(idx2) {
		t.Error("PermissionSet has a permission outside the key's scopes — service keys must not get any RBAC-derived permission")
	}
}

func TestAuthenticate_APIKeyScopeRestrictsBeyondUserPermissions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// f.userID's admin role already grants both testPermission and
	// testPermission2 (fixture setup) — the key here is scoped to only
	// one of them.
	fullKey, _, err := f.apiKeys.IssueKey(ctx, f.tenantID, &f.userID, "Scoped Key", []string{testPermission}, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	authCtx, err := f.checker.Authenticate(ctx, fullKey, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	idx1, _ := f.permissions.Index(testPermission)
	if !authCtx.PermissionSet.Has(idx1) {
		t.Error("PermissionSet does not have the key's own scope")
	}
	idx2, _ := f.permissions.Index(testPermission2)
	if authCtx.PermissionSet.Has(idx2) {
		t.Error("PermissionSet has a permission the user's role grants but the key's scopes don't — scopes must restrict, never expand")
	}
}

func TestAuthenticate_APIKeyUnknownKeyReturnsErrAPIKeyInvalid(t *testing.T) {
	f := newFixture(t)

	_, err := f.checker.Authenticate(context.Background(), "erp_notreal_notreal", f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("Authenticate() error = %v, want ErrAPIKeyInvalid", err)
	}
}

func TestAuthenticate_APIKeyRevokedKeyReturnsErrAPIKeyInvalid(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fullKey, key, err := f.apiKeys.IssueKey(ctx, f.tenantID, nil, "To Revoke", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}
	if err := f.apiKeys.Revoke(ctx, key.ID, "test"); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	_, err = f.checker.Authenticate(ctx, fullKey, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("Authenticate() error = %v, want ErrAPIKeyInvalid", err)
	}
}

func TestAuthenticate_APIKeyExpiredKeyReturnsErrAPIKeyExpired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	fullKey, _, err := f.apiKeys.IssueKey(ctx, f.tenantID, nil, "Expired Key", nil, nil, &past, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	_, err = f.checker.Authenticate(ctx, fullKey, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if !errors.Is(err, ErrAPIKeyExpired) {
		t.Errorf("Authenticate() error = %v, want ErrAPIKeyExpired", err)
	}
}

func TestAuthenticate_APIKeyDisallowedIPReturnsErrAPIKeyIPNotAllowed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fullKey, _, err := f.apiKeys.IssueKey(ctx, f.tenantID, nil, "IP Restricted", nil, []string{"10.0.0.1"}, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	_, err = f.checker.Authenticate(ctx, fullKey, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if !errors.Is(err, ErrAPIKeyIPNotAllowed) {
		t.Errorf("Authenticate() error = %v, want ErrAPIKeyIPNotAllowed", err)
	}
}

func TestAuthenticate_APIKeyTenantMismatchReturnsErrTenantMismatch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fullKey, _, err := f.apiKeys.IssueKey(ctx, f.tenantID, nil, "Mismatch", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	_, err = f.checker.Authenticate(ctx, fullKey, "00000000-0000-0000-0000-000000000000", f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if !errors.Is(err, ErrTenantMismatch) {
		t.Errorf("Authenticate() error = %v, want ErrTenantMismatch", err)
	}
}

func TestAuthenticate_APIKeyDisabledFlagFallsThroughToInvalidToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fullKey, _, err := f.apiKeys.IssueKey(ctx, f.tenantID, nil, "Disabled Flag", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	disabledChecker := &Checker{
		signingKey:    f.checker.signingKey,
		revoker:       f.checker.revoker,
		users:         f.checker.users,
		roles:         f.checker.roles,
		roleCache:     f.checker.roleCache,
		roleMap:       f.checker.roleMap,
		apiKeys:       f.checker.apiKeys,
		enableAPIKeys: false,
		mfaTokens:     f.checker.mfaTokens,
		mfaCreds:      f.checker.mfaCreds,
		mfaPolicies:   f.checker.mfaPolicies,
	}

	_, err = disabledChecker.Authenticate(ctx, fullKey, f.tenantID, f.tenantSlug, "203.0.113.1", f.permissions, nil)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Authenticate() error = %v, want ErrInvalidToken (erp_ branch should have nothing to dispatch to when disabled)", err)
	}
}

func TestAuthenticate_APIKeyUpdatesLastUsedAsync(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fullKey, key, err := f.apiKeys.IssueKey(ctx, f.tenantID, nil, "Track Usage", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	if _, err := f.checker.Authenticate(ctx, fullKey, f.tenantID, f.tenantSlug, "203.0.113.7", f.permissions, nil); err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var lastUsedAt sql.NullTime
		if err := f.conn.QueryRowContext(ctx, "SELECT last_used_at FROM system.api_keys WHERE id = $1", key.ID).Scan(&lastUsedAt); err != nil {
			t.Fatalf("query last_used_at: %v", err)
		}
		if lastUsedAt.Valid {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("last_used_at was not set within the deadline — the fire-and-forget UpdateLastUsed goroutine may not have run")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAuthenticate_APIKeyUpdatesLastUsedEvenWhenPermissionDenied(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Scoped narrower than what's required below — auth-internals.md §7
	// step 10 (update last_used_at) happens before step 11 (permission
	// evaluation), so a key that authenticates fine but is then denied
	// for a specific required permission must still get its last_used_at
	// updated.
	fullKey, key, err := f.apiKeys.IssueKey(ctx, f.tenantID, nil, "Denied But Used", []string{testPermission}, nil, nil, nil)
	if err != nil {
		t.Fatalf("IssueKey() error: %v", err)
	}

	_, err = f.checker.Authenticate(ctx, fullKey, f.tenantID, f.tenantSlug, "203.0.113.7", f.permissions, []string{testPermission2})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Authenticate() error = %v, want ErrPermissionDenied", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var lastUsedAt sql.NullTime
		if err := f.conn.QueryRowContext(ctx, "SELECT last_used_at FROM system.api_keys WHERE id = $1", key.ID).Scan(&lastUsedAt); err != nil {
			t.Fatalf("query last_used_at: %v", err)
		}
		if lastUsedAt.Valid {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("last_used_at was not set within the deadline — a permission-denied request must still count as key usage")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAuthenticateMFAToken_ValidTokenReturnsMFAPending(t *testing.T) {
	f := newFixture(t)
	token, _, err := f.mfaTokens.Issue(f.userID, f.tenantID, "https://example.com")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	authCtx, err := f.checker.AuthenticateMFAToken(token, f.tenantID)
	if err != nil {
		t.Fatalf("AuthenticateMFAToken() error: %v", err)
	}
	if !authCtx.MFAPending {
		t.Error("MFAPending = false, want true")
	}
	if authCtx.IsAuthenticated {
		t.Error("IsAuthenticated = true, want false — mid-login, not a fully authenticated principal")
	}
	if authCtx.UserID != f.userID {
		t.Errorf("UserID = %q, want %q", authCtx.UserID, f.userID)
	}
}

func TestAuthenticateMFAToken_ExpiredTokenIsRejected(t *testing.T) {
	f := newFixture(t)
	claims := mfatoken.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   f.userID,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
		TenantID: f.tenantID,
		Txn:      "expired-txn",
		Purpose:  mfatoken.PurposeMFALogin,
		Origin:   "https://example.com",
	}
	token := f.signRawMFAClaims(t, claims)

	_, err := f.checker.AuthenticateMFAToken(token, f.tenantID)
	if !errors.Is(err, mfatoken.ErrInvalidToken) {
		t.Errorf("AuthenticateMFAToken() error = %v, want mfatoken.ErrInvalidToken", err)
	}
}

func TestAuthenticateMFAToken_MalformedTokenIsRejected(t *testing.T) {
	f := newFixture(t)

	_, err := f.checker.AuthenticateMFAToken("not-a-jwt", f.tenantID)
	if !errors.Is(err, mfatoken.ErrInvalidToken) {
		t.Errorf("AuthenticateMFAToken() error = %v, want mfatoken.ErrInvalidToken", err)
	}
}

func TestAuthenticateMFAToken_TenantMismatchIsRejected(t *testing.T) {
	f := newFixture(t)
	token, _, err := f.mfaTokens.Issue(f.userID, f.tenantID, "https://example.com")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	_, err = f.checker.AuthenticateMFAToken(token, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrMFATokenTenantMismatch) {
		t.Errorf("AuthenticateMFAToken() error = %v, want ErrMFATokenTenantMismatch", err)
	}
}

// signRawMFAClaims signs claims with the fixture's real mfa_token key,
// bypassing mfatoken.Codec.Issue — needed for test cases (expired, etc.)
// Issue's own API doesn't let a caller construct.
func (f *fixture) signRawMFAClaims(t *testing.T, claims mfatoken.Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = f.mfaTokenKey.KeyID
	signed, err := tok.SignedString(f.mfaTokenKey.Secret)
	if err != nil {
		t.Fatalf("sign mfa claims: %v", err)
	}
	return signed
}

func TestEnforceMFA_ExemptRouteAllowsWithoutLoadingPolicy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A policy that would otherwise deny everything (required, no
	// enrollment) — proves the exempt-route short-circuit happens before
	// policy loading or enrollment lookup, not just that it happens to
	// allow.
	if err := f.tenantConfig.Set(ctx, f.tenantID, "mfa.enforcement_mode", string(enforce.ModeRequired)); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	decision, err := f.checker.EnforceMFA(ctx, "/auth/mfa/verify", f.tenantID, &AuthContext{UserID: f.userID})
	if err != nil {
		t.Fatalf("EnforceMFA() error: %v", err)
	}
	if decision != enforce.Allowed {
		t.Errorf("EnforceMFA() = %q, want Allowed", decision)
	}
}

func TestEnforceMFA_OptionalPolicyAllows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	decision, err := f.checker.EnforceMFA(ctx, "/widgets", f.tenantID, &AuthContext{UserID: f.userID})
	if err != nil {
		t.Fatalf("EnforceMFA() error: %v", err)
	}
	if decision != enforce.Allowed {
		t.Errorf("EnforceMFA() = %q, want Allowed for the default optional policy", decision)
	}
}

func TestEnforceMFA_RequiredAndUnenrolledReturnsSetupRequired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.tenantConfig.Set(ctx, f.tenantID, "mfa.enforcement_mode", string(enforce.ModeRequired)); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	decision, err := f.checker.EnforceMFA(ctx, "/widgets", f.tenantID, &AuthContext{UserID: f.userID})
	if err != nil {
		t.Fatalf("EnforceMFA() error: %v", err)
	}
	if decision != enforce.SetupRequired {
		t.Errorf("EnforceMFA() = %q, want SetupRequired", decision)
	}
}

func TestEnforceMFA_RequiredAndEnrolledButAMRMissingFactorReturnsFactorRequired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.tenantConfig.Set(ctx, f.tenantID, "mfa.enforcement_mode", string(enforce.ModeRequired)); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if _, err := f.mfaCreds.Insert(ctx, f.userID, mfa.CredentialTOTP, []byte("opaque"), nil); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}

	decision, err := f.checker.EnforceMFA(ctx, "/widgets", f.tenantID, &AuthContext{UserID: f.userID, AMR: []string{"pwd"}})
	if err != nil {
		t.Fatalf("EnforceMFA() error: %v", err)
	}
	if decision != enforce.FactorRequired {
		t.Errorf("EnforceMFA() = %q, want FactorRequired", decision)
	}
}

func TestEnforceMFA_AssuranceAgedPastPolicyReturnsReverifyRequired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.tenantConfig.Set(ctx, f.tenantID, "mfa.enforcement_mode", string(enforce.ModeRequired)); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if err := f.tenantConfig.Set(ctx, f.tenantID, "mfa.max_assurance_age_hours", "1"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if _, err := f.mfaCreds.Insert(ctx, f.userID, mfa.CredentialTOTP, []byte("opaque"), nil); err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	verifiedAt := time.Now().Add(-2 * time.Hour)

	decision, err := f.checker.EnforceMFA(ctx, "/widgets", f.tenantID, &AuthContext{
		UserID:        f.userID,
		AMR:           []string{"pwd", "totp"},
		MFAVerifiedAt: &verifiedAt,
	})
	if err != nil {
		t.Fatalf("EnforceMFA() error: %v", err)
	}
	if decision != enforce.ReverifyRequired {
		t.Errorf("EnforceMFA() = %q, want ReverifyRequired", decision)
	}
}

func TestEnforceMFA_RequiredForRolesOnlyAppliesToMatchingRole(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.tenantConfig.Set(ctx, f.tenantID, "mfa.enforcement_mode", string(enforce.ModeRequiredForRoles)); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if err := f.tenantConfig.Set(ctx, f.tenantID, "mfa.required_roles", "billing_admin"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	decision, err := f.checker.EnforceMFA(ctx, "/widgets", f.tenantID, &AuthContext{UserID: f.userID, RolesLive: []string{"admin"}})
	if err != nil {
		t.Fatalf("EnforceMFA() error: %v", err)
	}
	if decision != enforce.Allowed {
		t.Errorf("EnforceMFA() = %q, want Allowed for a role the policy doesn't require MFA for", decision)
	}

	decision, err = f.checker.EnforceMFA(ctx, "/widgets", f.tenantID, &AuthContext{UserID: f.userID, RolesLive: []string{"billing_admin"}})
	if err != nil {
		t.Fatalf("EnforceMFA() error: %v", err)
	}
	if decision != enforce.SetupRequired {
		t.Errorf("EnforceMFA() = %q, want SetupRequired for a role the policy does require MFA for", decision)
	}
}
