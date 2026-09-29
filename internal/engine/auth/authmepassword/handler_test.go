package authmepassword

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
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
	localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"
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
	checker    *authcheck.Checker
	tenants    *tenant.Store
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
	if err := roleStore.BootstrapMembershipIndex(ctx); err != nil {
		t.Fatalf("BootstrapMembershipIndex() error: %v", err)
	}
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
	handler := NewHandler(tenantResolver, authChecker, userStore, password.NewPolicyStore(configStore, role.NewStore(conn)), revoker, sessionStore, issuer, mailer, audit, password.NewHasher(1024, time.Second))

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
	hash, err := argon2id.CreateHash(oldPassword, password.ArgonParams)
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
	if _, err := conn.Exec(fmt.Sprintf("WITH m AS (INSERT INTO %[1]s.tenant_members (user_id) VALUES ($1) ON CONFLICT DO NOTHING) INSERT INTO %[1]s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID); err != nil {
		t.Fatalf("grant admin role: %v", err)
	}

	return &fixture{
		handler:    handler,
		issuer:     issuer,
		checker:    authChecker,
		tenants:    tenantStore,
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
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sessions: %v", err)
	}
	return states
}

// refreshHash matches authtoken's stored refresh_hash (hex SHA-256).
func refreshHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func TestServeHTTP_ChangesPassword(t *testing.T) {
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
	if len(f.mailer.sent) != 1 || f.mailer.sent[0] != f.email {
		t.Errorf("emails = %v, want one to %s", f.mailer.sent, f.email)
	}
	if len(f.audit.rows) != 1 || f.audit.rows[0].EventType != "password.changed" {
		t.Fatalf("audit rows = %+v, want one password.changed", f.audit.rows)
	}
	if row := f.audit.rows[0]; row.UserID != f.userID || row.ActorUserID != f.userID {
		t.Errorf("password.changed user_id/actor_user_id = %q/%q, want the user as both", row.UserID, row.ActorUserID)
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
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate family: %v", err)
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
	tokens := f.signIn(t)

	rec := f.doChange(t, tokens.AccessToken, oldPassword, "password1234")
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "auth.password_too_weak" {
		t.Fatalf("status = %d, body = %s, want 422 auth.password_too_weak", rec.Code, rec.Body.String())
	}
	if got := minLengthDetail(t, rec); got != float64(password.Global.MinLength) {
		t.Errorf("details.min_length = %v, want %d", got, password.Global.MinLength)
	}
	if !f.passwordMatches(t, oldPassword) {
		t.Error("password changed despite failing the policy")
	}
}

// addOtherTenant makes the fixture user a member, in the membership
// index, of another tenant whose minimum is minLength.
func (f *fixture) addOtherTenant(t *testing.T, minLength string) {
	t.Helper()
	other, err := f.tenants.CreateTenant(t.Context(), fmt.Sprintf("authmepwother%d", time.Now().UnixNano()), "Other Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, other.ID) })
	if err := f.config.Set(t.Context(), other.ID, password.KeyMinLength, minLength); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}
	if _, err := f.conn.Exec(`INSERT INTO system.tenant_memberships (user_id, tenant_id) VALUES ($1, $2)`, f.userID, other.ID); err != nil {
		t.Fatalf("index membership: %v", err)
	}
}

func TestServeHTTP_ChecksTheCombinedMinimumOfEveryTenant(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyMinLength, "14"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}
	f.addOtherTenant(t, "18")
	tokens := f.signIn(t)

	// 16 characters: enough for this tenant, too short for the other.
	rec := f.doChange(t, tokens.AccessToken, oldPassword, "sixteen chars ok")
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "auth.password_too_weak" {
		t.Fatalf("status = %d, body = %s, want 422 auth.password_too_weak", rec.Code, rec.Body.String())
	}
	if got := minLengthDetail(t, rec); got != float64(18) {
		t.Errorf("details.min_length = %v, want the other tenant's 18", got)
	}
	if rec := f.doChange(t, tokens.AccessToken, oldPassword, newPassword); rec.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s, want 200 for a password meeting both", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_LiftsPasswordChangeRestriction(t *testing.T) {
	f := newFixture(t)
	tokens, err := f.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug, PasswordChangeRequired: true})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if _, err := f.checker.Authenticate(t.Context(), tokens.AccessToken, f.tenantID, f.tenantSlug, "", nil, nil); !errors.Is(err, authcheck.ErrPasswordChangeRequired) {
		t.Fatalf("Authenticate(restricted) error = %v, want ErrPasswordChangeRequired", err)
	}

	rec := f.doChange(t, tokens.AccessToken, oldPassword, newPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	reissued := accessTokenCookie(rec)
	if reissued == "" {
		t.Fatalf("headers = %v, want a reissued access token cookie", rec.Header())
	}
	authCtx, err := f.checker.Authenticate(t.Context(), reissued, f.tenantID, f.tenantSlug, "", nil, nil)
	if err != nil {
		t.Fatalf("Authenticate(reissued) error: %v", err)
	}
	if authCtx.PasswordChangeRequired {
		t.Error("reissued token still carries pcr")
	}

	// The restriction is off the session row, so a refresh stays clear.
	refreshed, _, err := f.issuer.Refresh(t.Context(), tokens.RefreshToken, authtoken.RefreshParams{})
	if err != nil || refreshed == nil {
		t.Fatalf("Refresh() = %v, %v", refreshed, err)
	}
	if _, err := f.checker.Authenticate(t.Context(), refreshed.AccessToken, f.tenantID, f.tenantSlug, "", nil, nil); err != nil {
		t.Errorf("Authenticate(refreshed) error = %v, want an unrestricted session", err)
	}
}

func TestServeHTTP_LiftsRestrictionAcrossARotatedFamily(t *testing.T) {
	f := newFixture(t)
	tokens, err := f.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug, PasswordChangeRequired: true})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	// A refresh lands before the change, from the access token's old row.
	rotated, _, err := f.issuer.Refresh(t.Context(), tokens.RefreshToken, authtoken.RefreshParams{})
	if err != nil || rotated == nil {
		t.Fatalf("Refresh() = %v, %v", rotated, err)
	}

	if rec := f.doChange(t, tokens.AccessToken, oldPassword, newPassword); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}

	refreshed, _, err := f.issuer.Refresh(t.Context(), rotated.RefreshToken, authtoken.RefreshParams{})
	if err != nil || refreshed == nil {
		t.Fatalf("Refresh() = %v, %v", refreshed, err)
	}
	if _, err := f.checker.Authenticate(t.Context(), refreshed.AccessToken, f.tenantID, f.tenantSlug, "", nil, nil); err != nil {
		t.Errorf("Authenticate(refreshed) error = %v, want the rotated row cleared too", err)
	}
}

func TestServeHTTP_UnrestrictedChangeKeepsTheAccessToken(t *testing.T) {
	f := newFixture(t)
	tokens := f.signIn(t)

	rec := f.doChange(t, tokens.AccessToken, oldPassword, newPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if got := accessTokenCookie(rec); got != "" {
		t.Error("access token reissued for an unrestricted session, want it left alone")
	}
}

func accessTokenCookie(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-access_token" {
			return c.Value
		}
	}
	return ""
}

func TestServeHTTP_NoTokenRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doChange(t, "", oldPassword, newPassword)
	if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "unauthenticated" {
		t.Fatalf("status = %d, body = %s, want 401 unauthenticated", rec.Code, rec.Body.String())
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

func TestServeHTTP_OverloadedReturns503AndChangesNothing(t *testing.T) {
	f := newFixture(t)
	tokens := f.signIn(t)
	f.handler.hasher = saturatedHasher(t)

	assertOverloaded(t, f.doChange(t, tokens.AccessToken, oldPassword, newPassword))
	if !f.passwordMatches(t, oldPassword) {
		t.Error("password changed despite an overloaded hasher")
	}
}

func minLengthDetail(t *testing.T, rec *httptest.ResponseRecorder) any {
	t.Helper()
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Details["min_length"]
}
