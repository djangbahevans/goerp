package authmepassword

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
	oldPassword      = "correct horse battery staple"
	newPassword      = "a brand new long passphrase"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []string
}

func (m *fakeMailer) SendPasswordChanged(_ context.Context, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, email)
	return nil
}

type fakeAudit struct {
	mu   sync.Mutex
	rows []authaudit.Row
}

func (a *fakeAudit) Insert(_ context.Context, row authaudit.Row) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, row)
	return nil
}

type fixture struct {
	handler    *Handler
	issuer     *authtoken.Issuer
	users      *user.Store
	config     *tenantconfig.Store
	mailer     *fakeMailer
	audit      *fakeAudit
	domain     string
	tenantID   string
	tenantSlug string
	userID     string
	email      string
	conn       *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := t.Context()

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
	configStore := tenantconfig.NewStore(conn)
	if err := configStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
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
	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	authChecker := authcheck.NewChecker(&signingKeySet.Active, revoker, userStore, roleStore, permcache.NewRoleCache(cacheClient), permcache.NewRolePermissionMap(), apiKeys, false, nil, nil, nil)
	mailer := &fakeMailer{}
	audit := &fakeAudit{}
	handler := NewHandler(tenantResolver, authChecker, userStore, password.NewPolicyStore(configStore), revoker, mailer, audit)

	slug := fmt.Sprintf("authmepwtest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Auth Me Password Test Co")
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
	hash, err := password.Hash(oldPassword)
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}
	if _, err := conn.Exec(`UPDATE system.users SET status = 'active', password_hash = $2 WHERE id = $1`, userID, hash); err != nil {
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
		users:      userStore,
		config:     configStore,
		mailer:     mailer,
		audit:      audit,
		domain:     domain,
		tenantID:   tt.ID,
		tenantSlug: slug,
		userID:     userID,
		email:      email,
		conn:       conn,
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

func (f *fixture) signIn(t *testing.T) *authtoken.Tokens {
	t.Helper()
	tokens, err := f.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens
}

func (f *fixture) doChange(t *testing.T, accessToken, current, next string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(map[string]string{"current_password": current, "new_password": next})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/me/change-password", bytes.NewReader(b))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func (f *fixture) passwordMatches(t *testing.T, plain string) bool {
	t.Helper()
	u, err := f.users.GetByID(t.Context(), f.userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	ok, err := argon2id.ComparePasswordAndHash(plain, *u.PasswordHash)
	if err != nil {
		t.Fatalf("ComparePasswordAndHash() error: %v", err)
	}
	return ok
}

func (f *fixture) sessionStates(t *testing.T) map[string]string {
	t.Helper()
	rows, err := f.conn.Query(`SELECT id, COALESCE(revoke_reason, '') FROM system.sessions WHERE user_id = $1`, f.userID)
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	defer func() { _ = rows.Close() }()
	states := map[string]string{}
	for rows.Next() {
		var id, reason string
		if err := rows.Scan(&id, &reason); err != nil {
			t.Fatalf("scan session: %v", err)
		}
		states[id] = reason
	}
	return states
}

// refreshHash matches authtoken's stored refresh_hash (hex SHA-256).
func refreshHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func TestServeHTTP_ChangesPasswordAndRecordsPolicy(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyMinLength, "14"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}
	tokens := f.signIn(t)

	rec := f.doChange(t, tokens.AccessToken, oldPassword, newPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !f.passwordMatches(t, newPassword) || f.passwordMatches(t, oldPassword) {
		t.Error("stored hash should match only the new password")
	}
	u, err := f.users.GetByID(t.Context(), f.userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if u.PasswordSetAtPolicyTenantID == nil || *u.PasswordSetAtPolicyTenantID != f.tenantID || u.PasswordSetAtPolicyVersion != 1 {
		t.Errorf("policy tenant/version = %v/%d, want %s/1", u.PasswordSetAtPolicyTenantID, u.PasswordSetAtPolicyVersion, f.tenantID)
	}
	if len(f.mailer.sent) != 1 || f.mailer.sent[0] != f.email {
		t.Errorf("emails = %v, want one to %s", f.mailer.sent, f.email)
	}
	if len(f.audit.rows) != 1 || f.audit.rows[0].EventType != "password.changed" {
		t.Errorf("audit rows = %+v, want one password.changed", f.audit.rows)
	}
}

func (f *fixture) familyRevokeReasons(t *testing.T, familyOfRefreshHash string) []string {
	t.Helper()
	rows, err := f.conn.Query(`
		SELECT COALESCE(revoke_reason, '') FROM system.sessions
		WHERE family_id = (SELECT family_id FROM system.sessions WHERE refresh_hash = $1)
		ORDER BY created_at
	`, familyOfRefreshHash)
	if err != nil {
		t.Fatalf("query family: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var reasons []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatalf("scan: %v", err)
		}
		reasons = append(reasons, r)
	}
	return reasons
}

func TestServeHTTP_RevokesOtherSessionsKeepsCallersFamily(t *testing.T) {
	f := newFixture(t)
	other := f.signIn(t)
	caller := f.signIn(t)
	// Rotate the caller's family so it holds a rotated row plus a live one.
	rotated, _, err := f.issuer.Refresh(t.Context(), caller.RefreshToken, authtoken.RefreshParams{})
	if err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}

	rec := f.doChange(t, rotated.AccessToken, oldPassword, newPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}

	if got := f.familyRevokeReasons(t, refreshHash(other.RefreshToken)); !slices.Equal(got, []string{"password_change"}) {
		t.Errorf("other family revoke reasons = %q, want [password_change]", got)
	}
	if got := f.familyRevokeReasons(t, refreshHash(rotated.RefreshToken)); !slices.Equal(got, []string{"", ""}) {
		t.Errorf("caller family revoke reasons = %q, want both rows untouched", got)
	}

	// The caller's session still authenticates.
	if again := f.doChange(t, rotated.AccessToken, "wrong password", newPassword); errorCode(t, again) != "invalid_password" {
		t.Errorf("follow-up code = %q, want invalid_password (still authenticated)", errorCode(t, again))
	}
}

func TestServeHTTP_WrongCurrentPasswordLeavesEverythingAlone(t *testing.T) {
	f := newFixture(t)
	f.signIn(t)
	tokens := f.signIn(t)

	rec := f.doChange(t, tokens.AccessToken, "not my password", newPassword)
	if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "invalid_password" {
		t.Fatalf("status = %d, body = %s, want 401 invalid_password", rec.Code, rec.Body.String())
	}
	if !f.passwordMatches(t, oldPassword) {
		t.Error("password changed despite a wrong current password")
	}
	u, err := f.users.GetByID(t.Context(), f.userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if u.FailedLoginCount != 0 {
		t.Errorf("failed_login_count = %d, want 0", u.FailedLoginCount)
	}
	for key, reason := range f.sessionStates(t) {
		if reason != "" {
			t.Errorf("session %s revoked (%s), want untouched", key, reason)
		}
	}
}

func TestServeHTTP_WeakPasswordReturns422(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyRequireDigit, "true"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}
	tokens := f.signIn(t)

	rec := f.doChange(t, tokens.AccessToken, oldPassword, newPassword)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "auth.password_too_weak" {
		t.Fatalf("status = %d, body = %s, want 422 auth.password_too_weak", rec.Code, rec.Body.String())
	}
	if !f.passwordMatches(t, oldPassword) {
		t.Error("password changed despite failing the tenant policy")
	}
}

func TestServeHTTP_NoTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doChange(t, "", oldPassword, newPassword)
	if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "unauthenticated" {
		t.Fatalf("status = %d, body = %s, want 401 unauthenticated", rec.Code, rec.Body.String())
	}
}
