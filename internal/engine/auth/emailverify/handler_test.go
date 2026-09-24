package emailverify

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

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

type sentLink struct{ email, tenant, rawToken string }

type fakeMailer struct {
	mu    sync.Mutex
	links []sentLink
	sent  chan struct{}
}

func newFakeMailer() *fakeMailer { return &fakeMailer{sent: make(chan struct{}, 16)} }

func (m *fakeMailer) SendVerifyEmail(_ context.Context, email, tenantSlug, rawToken string) error {
	m.mu.Lock()
	m.links = append(m.links, sentLink{email, tenantSlug, rawToken})
	m.mu.Unlock()
	m.sent <- struct{}{}
	return nil
}

func (m *fakeMailer) waitSent(t *testing.T) sentLink {
	t.Helper()
	select {
	case <-m.sent:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a verification email")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.links[len(m.links)-1]
}

// assertNoneSent waits briefly, since sends run on a detached goroutine.
func (m *fakeMailer) assertNoneSent(t *testing.T) {
	t.Helper()
	select {
	case <-m.sent:
		t.Fatal("a verification email was sent, want none")
	case <-time.After(100 * time.Millisecond):
	}
}

type fixture struct {
	confirm    *ConfirmHandler
	resend     *ResendHandler
	mailer     *fakeMailer
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

	slug := fmt.Sprintf("verifytest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Verify Email Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	email := slug + "@example.com"
	t.Cleanup(func() { _ = cacheClient.Delete(context.Background(), rateLimitKey(email)) })
	userID, err := userStore.CreateRegistered(ctx, email, "unused-hash", user.StatusPendingVerification)
	if err != nil {
		t.Fatalf("CreateRegistered() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.user_mfa WHERE user_id = $1`, userID) })

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

	issuer := authtoken.NewIssuer(&keySet.Active, tenantStore, roleStore, sessionStore)
	mailer := newFakeMailer()

	return &fixture{
		confirm:    NewConfirmHandler(userStore, tenantStore, roleStore, mfaStore, issuer),
		resend:     NewResendHandler(userStore, tenantStore, roleStore, cacheClient, mailer),
		mailer:     mailer,
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

func (f *fixture) doConfirm(t *testing.T, token string) *httptest.ResponseRecorder {
	t.Helper()
	return f.doConfirmIn(t, f.tenantSlug, token)
}

func (f *fixture) doConfirmIn(t *testing.T, tenantSlug, token string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, f.confirm, "/auth/verify-email", map[string]any{"token": token, "tenant": tenantSlug}, map[string]string{"X-Client-Type": "cli"})
}

func (f *fixture) doResend(t *testing.T, email, tenantSlug string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, f.resend, "/auth/verify-email/resend", map[string]any{"email": email, "tenant": tenantSlug}, nil)
}

func (f *fixture) issueToken(t *testing.T) string {
	t.Helper()
	raw, err := IssueToken(t.Context(), f.users, f.userID)
	if err != nil {
		t.Fatalf("IssueToken() error: %v", err)
	}
	return raw
}

func (f *fixture) setStatus(t *testing.T, status user.Status) {
	t.Helper()
	if _, err := f.conn.Exec(`UPDATE system.users SET status = $2 WHERE id = $1`, f.userID, status); err != nil {
		t.Fatalf("set status: %v", err)
	}
}

type storedState struct {
	status        user.Status
	emailVerified bool
	tokenSet      bool
}

func (f *fixture) state(t *testing.T) storedState {
	t.Helper()
	var s storedState
	if err := f.conn.QueryRow(`SELECT status, email_verified, email_verify_token IS NOT NULL FROM system.users WHERE id = $1`, f.userID).Scan(&s.status, &s.emailVerified, &s.tokenSet); err != nil {
		t.Fatalf("read user state: %v", err)
	}
	return s
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

func assertInvalidToken(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	e, _ := decodeBody(t, rec)["error"].(map[string]any)
	if rec.Code != http.StatusNotFound || e["code"] != "invalid_token" {
		t.Fatalf("status = %d, body = %s, want 404 invalid_token", rec.Code, rec.Body.String())
	}
}

func assertLoginRequired(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["login_required"] != true || body["access_token"] != nil {
		t.Errorf("body = %v, want login_required and no tokens", body)
	}
}

func TestConfirm_ValidToken_VerifiesActivatesAndSignsIn(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t)

	rec := f.doConfirm(t, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["access_token"] == nil || body["refresh_token"] == nil {
		t.Errorf("body = %v, want a signed-in session", body)
	}
	if s := f.state(t); s.status != user.StatusActive || !s.emailVerified || s.tokenSet {
		t.Errorf("state = %+v, want active, verified, token cleared", s)
	}
	if n := f.activeSessionCount(t); n != 1 {
		t.Errorf("active sessions = %d, want 1", n)
	}

	assertInvalidToken(t, f.doConfirm(t, token))
}

func TestConfirm_BrowserClient_SetsSessionCookies(t *testing.T) {
	f := newFixture(t)

	rec := do(t, f.confirm, "/auth/verify-email", map[string]any{"token": f.issueToken(t), "tenant": f.tenantSlug}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("no cookies set, want the session cookies")
	}
	if body := decodeBody(t, rec); body["access_token"] != nil {
		t.Errorf("body = %v, want no tokens in a browser response", body)
	}
}

func TestConfirm_ConcurrentConfirms_OnlyOneSucceeds(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t)

	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { codes[i] = f.doConfirm(t, token).Code })
	}
	wg.Wait()

	ok := 0
	for _, c := range codes {
		if c == http.StatusOK {
			ok++
		}
	}
	if ok != 1 {
		t.Errorf("status codes = %v, want exactly one 200", codes)
	}
}

func TestConfirm_ExpiredToken_Returns404(t *testing.T) {
	f := newFixture(t)
	token := f.issueToken(t)
	if _, err := f.conn.Exec(`UPDATE system.users SET email_verify_expiry = NOW() - interval '1 second' WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("expire token: %v", err)
	}

	assertInvalidToken(t, f.doConfirm(t, token))
	if s := f.state(t); s.status != user.StatusPendingVerification || s.emailVerified {
		t.Errorf("state = %+v, want still pending and unverified", s)
	}
}

func TestConfirm_UnknownToken_Returns404(t *testing.T) {
	f := newFixture(t)
	assertInvalidToken(t, f.doConfirm(t, "not-a-real-token"))
}

func TestConfirm_PasswordResetToken_DoesNotVerify(t *testing.T) {
	f := newFixture(t)
	const raw = "a-password-reset-token"
	if err := f.users.SetPasswordResetToken(t.Context(), f.userID, hashToken(raw), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SetPasswordResetToken() error: %v", err)
	}

	assertInvalidToken(t, f.doConfirm(t, raw))
	if s := f.state(t); s.emailVerified {
		t.Error("email verified by a password reset token")
	}
}

func TestConfirm_ActiveAccount_VerifiesWithoutStatusChange(t *testing.T) {
	f := newFixture(t)
	f.setStatus(t, user.StatusActive)

	if rec := f.doConfirm(t, f.issueToken(t)); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if s := f.state(t); s.status != user.StatusActive || !s.emailVerified {
		t.Errorf("state = %+v, want active and verified", s)
	}
}

func TestConfirm_SuspendedAccount_VerifiesWithoutSigningInOrActivating(t *testing.T) {
	f := newFixture(t)
	f.setStatus(t, user.StatusSuspended)

	assertLoginRequired(t, f.doConfirm(t, f.issueToken(t)))
	if s := f.state(t); s.status != user.StatusSuspended || !s.emailVerified {
		t.Errorf("state = %+v, want suspended and verified", s)
	}
	if n := f.activeSessionCount(t); n != 0 {
		t.Errorf("active sessions = %d, want 0", n)
	}
}

func TestConfirm_MFAEnrolled_VerifiesWithoutSigningIn(t *testing.T) {
	f := newFixture(t)
	if _, err := f.mfaStore.Insert(t.Context(), f.userID, mfa.CredentialTOTP, []byte("x"), nil); err != nil {
		t.Fatalf("Insert() mfa credential error: %v", err)
	}

	assertLoginRequired(t, f.doConfirm(t, f.issueToken(t)))
	if s := f.state(t); s.status != user.StatusActive || !s.emailVerified {
		t.Errorf("state = %+v, want active and verified", s)
	}
	if n := f.activeSessionCount(t); n != 0 {
		t.Errorf("active sessions = %d, want 0", n)
	}
}

func TestConfirm_NoResolvableMembership_VerifiesWithoutSigningIn(t *testing.T) {
	for name, slug := range map[string]string{"unknown tenant": "nosuchtenant", "no tenant": ""} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)

			assertLoginRequired(t, f.doConfirmIn(t, slug, f.issueToken(t)))
			if s := f.state(t); s.status != user.StatusActive || !s.emailVerified {
				t.Errorf("state = %+v, want active and verified", s)
			}
			if n := f.activeSessionCount(t); n != 0 {
				t.Errorf("active sessions = %d, want 0", n)
			}
		})
	}
}

func TestResend_PendingMember_SendsFreshLinkAndInvalidatesOld(t *testing.T) {
	f := newFixture(t)
	oldToken := f.issueToken(t)

	start := time.Now()
	rec := f.doResend(t, f.email, f.tenantSlug)
	if rec.Code != http.StatusOK || decodeBody(t, rec)["status"] != "ok" {
		t.Fatalf("status = %d, body = %s, want 200 ok", rec.Code, rec.Body.String())
	}
	if elapsed := time.Since(start); elapsed < minResendResponseTime {
		t.Errorf("response took %v, want at least %v", elapsed, minResendResponseTime)
	}
	link := f.mailer.waitSent(t)
	if link.email != f.email || link.tenant != f.tenantSlug || link.rawToken == oldToken {
		t.Errorf("sent link = %+v, want a fresh token for %s in %s", link, f.email, f.tenantSlug)
	}

	assertInvalidToken(t, f.doConfirm(t, oldToken))
	if rec := f.doConfirm(t, link.rawToken); rec.Code != http.StatusOK {
		t.Errorf("confirm with the resent token status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestResend_SendsNothingOutsidePendingMembership(t *testing.T) {
	cases := map[string]func(t *testing.T, f *fixture) (email, tenantSlug string){
		"unknown email":  func(_ *testing.T, f *fixture) (string, string) { return "nobody-" + f.email, f.tenantSlug },
		"unknown tenant": func(_ *testing.T, f *fixture) (string, string) { return f.email, "nosuchtenant" },
		"already active": func(t *testing.T, f *fixture) (string, string) {
			f.setStatus(t, user.StatusActive)
			return f.email, f.tenantSlug
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			email, tenantSlug := setup(t, f)

			rec := f.doResend(t, email, tenantSlug)
			if rec.Code != http.StatusOK || decodeBody(t, rec)["status"] != "ok" {
				t.Fatalf("status = %d, body = %s, want 200 ok", rec.Code, rec.Body.String())
			}
			f.mailer.assertNoneSent(t)
		})
	}
}

func TestResend_NotATenantMember_SendsNothing(t *testing.T) {
	f := newFixture(t)
	otherSlug := fmt.Sprintf("verifyother%d", time.Now().UnixNano())
	other, err := tenant.NewStore(f.conn).CreateTenant(t.Context(), otherSlug, "Other Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, other.ID) })
	schema := tenantschema.Name(otherSlug)
	if _, err := f.conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create other tenant schema: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)) })
	if err := role.NewStore(f.conn).Bootstrap(t.Context(), otherSlug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}

	if rec := f.doResend(t, f.email, otherSlug); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	f.mailer.assertNoneSent(t)
}

func TestResend_RateLimitedAfterThreePerEmail(t *testing.T) {
	f := newFixture(t)
	for range resendsPerEmail {
		f.doResend(t, f.email, f.tenantSlug)
		f.mailer.waitSent(t)
	}

	if rec := f.doResend(t, f.email, f.tenantSlug); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	f.mailer.assertNoneSent(t)
}
