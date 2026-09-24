package passwordreset

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
	oldPassword      = "correct horse battery staple"
	newPassword      = "a brand new long passphrase"
)

type sentReset struct{ email, tenant, rawToken string }

type fakeMailer struct {
	mu        sync.Mutex
	resets    []sentReset
	confirmed []string
	sent      chan struct{}
}

func newFakeMailer() *fakeMailer { return &fakeMailer{sent: make(chan struct{}, 16)} }

func (m *fakeMailer) SendPasswordReset(_ context.Context, email, tenantSlug, rawToken string) error {
	m.mu.Lock()
	m.resets = append(m.resets, sentReset{email, tenantSlug, rawToken})
	m.mu.Unlock()
	m.sent <- struct{}{}
	return nil
}

func (m *fakeMailer) SendPasswordResetConfirmed(_ context.Context, email string) error {
	m.mu.Lock()
	m.confirmed = append(m.confirmed, email)
	m.mu.Unlock()
	m.sent <- struct{}{}
	return nil
}

func (m *fakeMailer) waitSent(t *testing.T) {
	t.Helper()
	select {
	case <-m.sent:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an email send")
	}
}

// assertNoneSent waits briefly, since sends run on a detached goroutine.
func (m *fakeMailer) assertNoneSent(t *testing.T) {
	t.Helper()
	select {
	case <-m.sent:
		t.Fatal("an email was sent, want none")
	case <-time.After(100 * time.Millisecond):
	}
}

func (m *fakeMailer) lastReset(t *testing.T) sentReset {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.resets) == 0 {
		t.Fatal("no reset email sent")
	}
	return m.resets[len(m.resets)-1]
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

func (a *fakeAudit) eventTypes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, r := range a.rows {
		out = append(out, r.EventType)
	}
	return out
}

type fixture struct {
	request    *RequestHandler
	confirm    *ConfirmHandler
	mailer     *fakeMailer
	audit      *fakeAudit
	issuer     *authtoken.Issuer
	users      *user.Store
	mfaStore   *mfa.Store
	conn       *sql.DB
	tenantSlug string
	userID     string
	email      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := t.Context()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockSigningKeyTable(t, conn)

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
	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	keySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}
	roleStore := role.NewStore(conn)

	slug := fmt.Sprintf("pwresettest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Password Reset Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	email := slug + "@example.com"
	t.Cleanup(func() { _ = cacheClient.Delete(context.Background(), rateLimitKey(email)) })
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
	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	mailer := newFakeMailer()
	audit := &fakeAudit{}

	return &fixture{
		request:    NewRequestHandler(userStore, tenantStore, roleStore, cacheClient, mailer, audit),
		confirm:    NewConfirmHandler(userStore, tenantStore, roleStore, mfaStore, revoker, issuer, mailer, audit),
		mailer:     mailer,
		audit:      audit,
		issuer:     issuer,
		users:      userStore,
		mfaStore:   mfaStore,
		conn:       conn,
		tenantSlug: slug,
		userID:     userID,
		email:      email,
	}
}

// lockSigningKeyTable serializes against every other package's test
// touching the shared system.jwt_signing_keys table.
func lockSigningKeyTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := t.Context()
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

func do(t *testing.T, h http.Handler, path string, body map[string]any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.RemoteAddr = "203.0.113.7:54321"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (f *fixture) doRequest(t *testing.T, email, tenantSlug string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, f.request, "/auth/password-reset/request", map[string]any{"email": email, "tenant": tenantSlug}, nil)
}

func (f *fixture) doConfirm(t *testing.T, token, newPw string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, f.confirm, "/auth/password-reset/confirm", map[string]any{
		"token": token, "new_password": newPw, "tenant": f.tenantSlug,
	}, map[string]string{"X-Client-Type": "cli"})
}

// issueToken runs a real request and returns the emailed raw token.
func (f *fixture) issueToken(t *testing.T) string {
	t.Helper()
	if rec := f.doRequest(t, f.email, f.tenantSlug); rec.Code != http.StatusOK {
		t.Fatalf("request status = %d, body = %s", rec.Code, rec.Body.String())
	}
	f.mailer.waitSent(t)
	return f.mailer.lastReset(t).rawToken
}

func (f *fixture) storedHash(t *testing.T) string {
	t.Helper()
	var h string
	if err := f.conn.QueryRow(`SELECT password_hash FROM system.users WHERE id = $1`, f.userID).Scan(&h); err != nil {
		t.Fatalf("read password_hash: %v", err)
	}
	return h
}

func (f *fixture) passwordMatches(t *testing.T, plain string) bool {
	t.Helper()
	ok, err := argon2id.ComparePasswordAndHash(plain, f.storedHash(t))
	if err != nil {
		t.Fatalf("ComparePasswordAndHash() error: %v", err)
	}
	return ok
}

func (f *fixture) activeSessionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.conn.QueryRow(`SELECT count(*) FROM system.sessions WHERE user_id = $1 AND revoked_at IS NULL`, f.userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
	}
	return v
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	e, _ := decodeBody(t, rec)["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestRequest_KnownEmail_StoresHashedTokenAndEmailsLink(t *testing.T) {
	f := newFixture(t)

	rec := f.doRequest(t, f.email, f.tenantSlug)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	f.mailer.waitSent(t)
	sent := f.mailer.lastReset(t)
	if sent.email != f.email || sent.tenant != f.tenantSlug {
		t.Errorf("sent = %+v, want email %q tenant %q", sent, f.email, f.tenantSlug)
	}

	var storedToken string
	var expiry time.Time
	if err := f.conn.QueryRow(`SELECT password_reset_token, password_reset_expiry FROM system.users WHERE id = $1`, f.userID).Scan(&storedToken, &expiry); err != nil {
		t.Fatalf("read reset token: %v", err)
	}
	if storedToken != hashToken(sent.rawToken) {
		t.Error("stored token is not the SHA-256 of the emailed raw token")
	}
	if until := time.Until(expiry); until < 59*time.Minute || until > time.Hour {
		t.Errorf("expiry in %v, want ~1h", until)
	}
	if got := f.audit.eventTypes(); len(got) != 1 || got[0] != "password.reset_requested" {
		t.Errorf("audit events = %v, want [password.reset_requested]", got)
	}
}

func TestRequest_UnknownEmail_SameResponseAsKnown(t *testing.T) {
	f := newFixture(t)

	known := f.doRequest(t, f.email, f.tenantSlug)
	f.mailer.waitSent(t)
	unknown := f.doRequest(t, "nobody-"+f.email, f.tenantSlug)

	if unknown.Code != known.Code || unknown.Body.String() != known.Body.String() {
		t.Errorf("unknown = %d %s, known = %d %s, want identical", unknown.Code, unknown.Body.String(), known.Code, known.Body.String())
	}
	f.mailer.assertNoneSent(t)
}

func TestRequest_NotATenantMember_IssuesNothing(t *testing.T) {
	f := newFixture(t)

	rec := f.doRequest(t, f.email, "no-such-tenant")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	f.mailer.assertNoneSent(t)

	var token sql.NullString
	if err := f.conn.QueryRow(`SELECT password_reset_token FROM system.users WHERE id = $1`, f.userID).Scan(&token); err != nil {
		t.Fatalf("read reset token: %v", err)
	}
	if token.Valid {
		t.Error("a reset token was stored for a tenant the user doesn't belong to")
	}
}

func TestRequest_RateLimitedAfterThreePerEmail(t *testing.T) {
	f := newFixture(t)

	for range requestsPerEmail {
		f.issueToken(t)
	}
	rec := f.doRequest(t, f.email, f.tenantSlug)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when rate limited", rec.Code)
	}
	f.mailer.assertNoneSent(t)
}

func TestConfirm_ValidToken_ResetsPasswordRevokesSessionsAndSignsIn(t *testing.T) {
	f := newFixture(t)
	if _, err := f.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug}); err != nil {
		t.Fatalf("Issue() pre-existing session error: %v", err)
	}
	token := f.issueToken(t)

	rec := f.doConfirm(t, token, newPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["access_token"] == nil || body["refresh_token"] == nil {
		t.Errorf("body = %v, want a signed-in session", body)
	}
	if !f.passwordMatches(t, newPassword) {
		t.Error("stored hash doesn't match the new password")
	}
	if n := f.activeSessionCount(t); n != 1 {
		t.Errorf("active sessions = %d, want 1 (the pre-existing one revoked, the new one issued)", n)
	}
	var reason string
	if err := f.conn.QueryRow(`SELECT revoke_reason FROM system.sessions WHERE user_id = $1 AND revoked_at IS NOT NULL`, f.userID).Scan(&reason); err != nil {
		t.Fatalf("read revoke_reason: %v", err)
	}
	if reason != "password_change" {
		t.Errorf("revoke_reason = %q, want password_change", reason)
	}
	f.mailer.waitSent(t)
	if len(f.mailer.confirmed) != 1 {
		t.Errorf("confirmation emails = %v, want one", f.mailer.confirmed)
	}

	reuse := f.doConfirm(t, token, "yet another long passphrase")
	if reuse.Code != http.StatusNotFound {
		t.Errorf("reuse status = %d, want 404", reuse.Code)
	}
}

func TestConfirm_ExpiredToken_Returns404(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t)
	if _, err := f.conn.Exec(`UPDATE system.users SET password_reset_expiry = NOW() - interval '1 second' WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("expire token: %v", err)
	}

	rec := f.doConfirm(t, token, newPassword)
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "invalid_token" {
		t.Fatalf("status = %d, body = %s, want 404 invalid_token", rec.Code, rec.Body.String())
	}
	if !f.passwordMatches(t, oldPassword) {
		t.Error("password changed on an expired token")
	}
}

func TestConfirm_UnknownToken_Returns404(t *testing.T) {
	f := newFixture(t)

	rec := f.doConfirm(t, "not-a-real-token", newPassword)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestConfirm_WeakPassword_Returns422AndKeepsToken(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t)

	rec := f.doConfirm(t, token, "short")
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "auth.password_too_weak" {
		t.Fatalf("status = %d, body = %s, want 422 auth.password_too_weak", rec.Code, rec.Body.String())
	}
	if !f.passwordMatches(t, oldPassword) {
		t.Error("password changed despite failing validation")
	}

	if retry := f.doConfirm(t, token, newPassword); retry.Code != http.StatusOK {
		t.Errorf("retry status = %d, want 200 — a rejected password must not consume the token", retry.Code)
	}
}

func TestConfirm_MFAEnrolled_ResetsWithoutSigningIn(t *testing.T) {
	f := newFixture(t)
	if _, err := f.mfaStore.Insert(t.Context(), f.userID, mfa.CredentialTOTP, []byte("x"), nil); err != nil {
		t.Fatalf("Insert() mfa credential error: %v", err)
	}
	if _, err := f.conn.Exec(`UPDATE system.users SET failed_login_count = 10, locked_until = NOW() + interval '30 minutes' WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("lock user: %v", err)
	}
	token := f.issueToken(t)

	rec := f.doConfirm(t, token, newPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["login_required"] != true || body["access_token"] != nil {
		t.Errorf("body = %v, want login_required and no tokens", body)
	}
	if !f.passwordMatches(t, newPassword) {
		t.Error("password not reset")
	}
	if n := f.activeSessionCount(t); n != 0 {
		t.Errorf("active sessions = %d, want 0", n)
	}
	u, err := f.users.GetByID(t.Context(), f.userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if u.LockedUntil != nil || u.FailedLoginCount != 0 {
		t.Errorf("locked_until = %v, failed_login_count = %d, want the lockout cleared by the reset", u.LockedUntil, u.FailedLoginCount)
	}
}

func TestConfirm_SuspendedUser_ResetsWithoutSigningInOrChangingStatus(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t)
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'suspended' WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("suspend user: %v", err)
	}

	rec := f.doConfirm(t, token, newPassword)
	if rec.Code != http.StatusOK || decodeBody(t, rec)["login_required"] != true {
		t.Fatalf("status = %d, body = %s, want 200 login_required", rec.Code, rec.Body.String())
	}
	u, err := f.users.GetByID(t.Context(), f.userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if u.Status != user.StatusSuspended {
		t.Errorf("status = %q, want suspended", u.Status)
	}
	if n := f.activeSessionCount(t); n != 0 {
		t.Errorf("active sessions = %d, want 0", n)
	}
}
