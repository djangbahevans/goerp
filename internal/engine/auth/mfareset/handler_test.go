package mfareset

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
const testCallerPassword = "correct horse battery staple"

// spyMailer records SendMFAReset calls instead of sending real email.
type spyMailer struct {
	mu     sync.Mutex
	emails []string
}

func (m *spyMailer) SendMFAReset(ctx context.Context, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emails = append(m.emails, email)
	return nil
}

func (m *spyMailer) sentTo(email string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Contains(m.emails, email)
}

// spyAudit records Emit calls instead of writing to a real audit log.
type spyAudit struct {
	mu     sync.Mutex
	events []map[string]any
}

func (a *spyAudit) Emit(ctx context.Context, tenantSlug, eventName string, payload map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, map[string]any{"tenant": tenantSlug, "event": eventName, "payload": payload})
	return nil
}

func (a *spyAudit) called() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.events) > 0
}

type fixture struct {
	handler    *Handler
	issuer     *authtoken.Issuer
	roles      *role.Store
	mfa        *mfa.Store
	sessions   *session.Store
	mailer     *spyMailer
	audit      *spyAudit
	domain     string
	tenantID   string
	tenantSlug string
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

	tenantResolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)
	issuer := authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore)
	roleCache := permcache.NewRoleCache(cacheClient)
	roleMap := permcache.NewRolePermissionMap()
	registry := permission.NewPermissionRegistry()
	authChecker := authcheck.NewChecker(&signingKeySet.Active, sessionrevoke.NewRevoker(sessionStore, cacheClient), userStore, roleStore, roleCache, roleMap, apiKeys, false)
	sessionRevoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	mailer := &spyMailer{}
	audit := &spyAudit{}
	handler := NewHandler(tenantResolver, authChecker, userStore, roleStore, mfaStore, sessionRevoker, mailer, audit)

	slug := fmt.Sprintf("mfaresettest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "MFA Reset Test Co")
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

	if err := roleMap.RebuildAll(ctx, tenantStore, roleStore, registry); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}

	return &fixture{
		handler:    handler,
		issuer:     issuer,
		roles:      roleStore,
		mfa:        mfaStore,
		sessions:   sessionStore,
		mailer:     mailer,
		audit:      audit,
		domain:     domain,
		tenantID:   tt.ID,
		tenantSlug: slug,
		conn:       conn,
		cache:      cacheClient,
	}
}

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

// createUserWithRole creates an active user, grants roleName in the
// fixture's tenant, and registers cleanup.
func (f *fixture) createUserWithRole(t *testing.T, roleName string) (userID, email string) {
	t.Helper()
	ctx := context.Background()

	userStore := user.NewStore(f.conn)
	email = fmt.Sprintf("mfaresettest%d@example.com", time.Now().UnixNano())
	userID, err := userStore.FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.user_mfa WHERE user_id = $1`, userID) })
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })

	roleID, err := f.roles.GetRoleByName(ctx, f.tenantSlug, roleName)
	if err != nil {
		t.Fatalf("GetRoleByName(%q) error: %v", roleName, err)
	}
	schema := tenantschema.Name(f.tenantSlug)
	if _, err := f.conn.Exec(fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID); err != nil {
		t.Fatalf("grant role %q: %v", roleName, err)
	}

	return userID, email
}

// createCallerWithPassword creates an admin-role user with a real
// password hash, so confirmCallerPassword has something real to check.
func (f *fixture) createCallerWithPassword(t *testing.T) (userID string) {
	t.Helper()
	userID, _ = f.createUserWithRole(t, "admin")
	hash, err := argon2id.CreateHash(testCallerPassword, argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("CreateHash() error: %v", err)
	}
	if _, err := f.conn.Exec(`UPDATE system.users SET password_hash = $2 WHERE id = $1`, userID, hash); err != nil {
		t.Fatalf("set caller password: %v", err)
	}
	return userID
}

func (f *fixture) issueAccessToken(t *testing.T, userID string) string {
	t.Helper()
	tokens, err := f.issuer.Issue(context.Background(), authtoken.LoginParams{
		UserID:     userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func (f *fixture) doReset(t *testing.T, accessToken, targetID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/mfa/reset", bytes.NewReader(b))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	req = req.WithContext(route.WithParams(req.Context(), map[string]string{"id": targetID}))
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_Success_RevokesFactorsAndTenantSessionsNotifiesAndAudits(t *testing.T) {
	f := newFixture(t)
	callerID := f.createCallerWithPassword(t)
	callerToken := f.issueAccessToken(t, callerID)
	targetID, targetEmail := f.createUserWithRole(t, "user")

	cred, err := f.mfa.Insert(context.Background(), targetID, mfa.CredentialTOTP, []byte("x"), nil)
	if err != nil {
		t.Fatalf("Insert() mfa credential error: %v", err)
	}
	targetSessionTokens, err := f.issuer.Issue(context.Background(), authtoken.LoginParams{
		UserID: targetID, TenantSlug: f.tenantSlug, DeviceID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Issue() target session error: %v", err)
	}
	_ = targetSessionTokens

	rec := f.doReset(t, callerToken, targetID, map[string]any{"password": testCallerPassword})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var revokedAt sql.NullTime
	if err := f.conn.QueryRowContext(context.Background(),
		"SELECT revoked_at FROM system.user_mfa WHERE id = $1", cred.ID,
	).Scan(&revokedAt); err != nil {
		t.Fatalf("query mfa credential: %v", err)
	}
	if !revokedAt.Valid {
		t.Error("target's mfa credential revoked_at is NULL, want set")
	}

	var sessionRevokedCount int
	if err := f.conn.QueryRowContext(context.Background(),
		"SELECT count(*) FROM system.sessions WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NOT NULL",
		targetID, f.tenantID,
	).Scan(&sessionRevokedCount); err != nil {
		t.Fatalf("count revoked sessions: %v", err)
	}
	if sessionRevokedCount == 0 {
		t.Error("target has no revoked sessions in this tenant, want at least one")
	}

	if !f.mailer.sentTo(targetEmail) {
		t.Errorf("mailer did not send to %q", targetEmail)
	}
	if !f.audit.called() {
		t.Error("audit emitter was never called")
	}
}

func TestServeHTTP_WrongPasswordRejected(t *testing.T) {
	f := newFixture(t)
	callerID := f.createCallerWithPassword(t)
	callerToken := f.issueAccessToken(t, callerID)
	targetID, _ := f.createUserWithRole(t, "user")

	rec := f.doReset(t, callerToken, targetID, map[string]any{"password": "wrong-password"})

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_NonAdminCallerRejected(t *testing.T) {
	f := newFixture(t)
	callerID, _ := f.createUserWithRole(t, "user")
	hash, err := argon2id.CreateHash(testCallerPassword, argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("CreateHash() error: %v", err)
	}
	if _, err := f.conn.Exec(`UPDATE system.users SET password_hash = $2 WHERE id = $1`, callerID, hash); err != nil {
		t.Fatalf("set caller password: %v", err)
	}
	callerToken := f.issueAccessToken(t, callerID)
	targetID, _ := f.createUserWithRole(t, "user")

	rec := f.doReset(t, callerToken, targetID, map[string]any{"password": testCallerPassword})

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_TargetNotMemberOfTenantRejected(t *testing.T) {
	f := newFixture(t)
	callerID := f.createCallerWithPassword(t)
	callerToken := f.issueAccessToken(t, callerID)

	// A user who exists but was never granted any role in this tenant.
	userStore := user.NewStore(f.conn)
	email := fmt.Sprintf("outsider%d@example.com", time.Now().UnixNano())
	outsiderID, err := userStore.FindOrCreateInvited(context.Background(), email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, outsiderID) })

	rec := f.doReset(t, callerToken, outsiderID, map[string]any{"password": testCallerPassword})

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_NoAccessTokenRejected(t *testing.T) {
	f := newFixture(t)
	targetID, _ := f.createUserWithRole(t, "user")

	rec := f.doReset(t, "", targetID, map[string]any{"password": testCallerPassword})

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_UnresolvableHostRejected(t *testing.T) {
	f := newFixture(t)
	callerID := f.createCallerWithPassword(t)
	callerToken := f.issueAccessToken(t, callerID)
	targetID, _ := f.createUserWithRole(t, "user")

	b, _ := json.Marshal(map[string]any{"password": testCallerPassword})
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/mfa/reset", bytes.NewReader(b))
	req.Host = "no-such-tenant.goerp.test"
	req.Header.Set("Authorization", "Bearer "+callerToken)
	req = req.WithContext(route.WithParams(req.Context(), map[string]string{"id": targetID}))
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}
