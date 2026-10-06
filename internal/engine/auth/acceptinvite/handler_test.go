package acceptinvite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtest"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"
	newPassword      = "a brand new long passphrase"
)

// fakeMailer captures the raw invite token, which never leaves the invite
// store any other way.
type fakeMailer struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (m *fakeMailer) SendInvite(_ context.Context, email, _, rawToken string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[email] = rawToken
	return nil
}

type fakeAudit struct {
	mu     sync.Mutex
	events []auditEvent
}

type auditEvent struct {
	name, userID, actorUserID string
}

func (a *fakeAudit) Emit(_ context.Context, _, eventName, userID, actorUserID string, _ map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, auditEvent{eventName, userID, actorUserID})
	return nil
}

type fixture struct {
	handlers   *Handlers
	invites    *invite.Store
	users      *user.Store
	roles      *role.Store
	config     *tenantconfig.Store
	mailer     *fakeMailer
	audit      *fakeAudit
	conn       *sql.DB
	tenantID   string
	tenantSlug string
	tenantName string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := t.Context()

	conn := membershiptest.New(t)

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
	configStore := tenantconfig.NewStore(conn)
	if err := configStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}
	keySet := authtest.SigningKeys()
	roleStore := role.NewStore(conn)
	if err := roleStore.BootstrapMembershipIndex(ctx); err != nil {
		t.Fatalf("BootstrapMembershipIndex() error: %v", err)
	}

	slug := fmt.Sprintf("acceptinvitetest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Accept Invite Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
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

	mailer := &fakeMailer{tokens: map[string]string{}}
	audit := &fakeAudit{}
	invites := invite.NewStore(conn, userStore, roleStore, audit, mailer)
	if err := invites.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("invite Bootstrap() error: %v", err)
	}

	issuer := authtoken.NewIssuer(&keySet.Active, tenantStore, roleStore, sessionStore)
	handlers := NewHandlers(tenantStore, invites, userStore, password.NewPolicyStore(configStore, role.NewStore(conn)), password.NewHasher(256, time.Second), issuer)

	return &fixture{
		handlers:   handlers,
		invites:    invites,
		users:      userStore,
		roles:      roleStore,
		config:     configStore,
		mailer:     mailer,
		audit:      audit,
		conn:       conn,
		tenantID:   tt.ID,
		tenantSlug: slug,
		tenantName: tt.Name,
	}
}

// invite sends a real invitation and returns the invitee's user id and raw
// token.
func (f *fixture) invite(t *testing.T, email string) (userID, token string) {
	t.Helper()
	if _, err := f.invites.Invite(t.Context(), f.tenantSlug, email, "user", "Kwame Mensah", nil); err != nil {
		t.Fatalf("Invite() error: %v", err)
	}
	u, err := f.users.GetByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("GetByEmail() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, u.ID)
		_, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, u.ID)
	})
	return u.ID, f.mailer.tokens[email]
}

func (f *fixture) newEmail() string {
	return fmt.Sprintf("invitee%d@example.com", time.Now().UnixNano())
}

func (f *fixture) doInfo(t *testing.T, tenantSlug, token string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"token": {token}, "tenant": {tenantSlug}}
	req := httptest.NewRequest(http.MethodGet, "/auth/accept-invite/info?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	f.handlers.Info(rec, req)
	return rec
}

func (f *fixture) doAccept(t *testing.T, token, pw string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(map[string]string{"token": token, "tenant": f.tenantSlug, "password": pw})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/accept-invite", bytes.NewReader(b))
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("X-Client-Type", "cli")
	rec := httptest.NewRecorder()
	f.handlers.Accept(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return v
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	e, _ := decodeBody(t, rec)["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func (f *fixture) isMember(t *testing.T, userID string) bool {
	t.Helper()
	ok, err := f.roles.IsMember(t.Context(), f.tenantSlug, userID)
	if err != nil {
		t.Fatalf("IsMember() error: %v", err)
	}
	return ok
}

func TestInfo_NewInvitee(t *testing.T) {
	f := newFixture(t)
	email := f.newEmail()
	_, token := f.invite(t, email)

	rec := f.doInfo(t, f.tenantSlug, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["tenant_name"] != f.tenantName || body["email"] != email || body["name"] != "Kwame Mensah" || body["password_required"] != true {
		t.Errorf("body = %v", body)
	}
}

func TestInfo_DeadLinksReturn404(t *testing.T) {
	f := newFixture(t)
	email := f.newEmail()
	_, token := f.invite(t, email)

	cases := map[string]struct{ tenant, token string }{
		"unknown token":  {f.tenantSlug, "00ff"},
		"non-hex token":  {f.tenantSlug, "not-hex"},
		"wrong tenant":   {"no-such-tenant", token},
		"missing tenant": {"", token},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if rec := f.doInfo(t, c.tenant, c.token); rec.Code != http.StatusNotFound || errorCode(t, rec) != "invalid_invite" {
				t.Errorf("status = %d, body = %s, want 404 invalid_invite", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestInfo_ExpiredAndRevokedReturn404(t *testing.T) {
	f := newFixture(t)
	schema := tenantschema.Name(f.tenantSlug)

	expiredEmail := f.newEmail()
	_, expired := f.invite(t, expiredEmail)
	if _, err := f.conn.Exec(fmt.Sprintf(`UPDATE %s.tenant_invitations SET expires_at = NOW() - interval '1 second' WHERE email = $1`, schema), expiredEmail); err != nil {
		t.Fatalf("expire invitation: %v", err)
	}
	revokedEmail := f.newEmail()
	_, revoked := f.invite(t, revokedEmail)
	inv, err := f.invites.GetLiveByEmail(t.Context(), f.tenantSlug, revokedEmail)
	if err != nil {
		t.Fatalf("GetLiveByEmail() error: %v", err)
	}
	if err := f.invites.Revoke(t.Context(), f.tenantSlug, inv.ID, nil); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}

	for name, token := range map[string]string{"expired": expired, "revoked": revoked} {
		if rec := f.doInfo(t, f.tenantSlug, token); rec.Code != http.StatusNotFound {
			t.Errorf("%s info status = %d, want 404", name, rec.Code)
		}
		if rec := f.doAccept(t, token, newPassword); rec.Code != http.StatusNotFound {
			t.Errorf("%s accept status = %d, want 404", name, rec.Code)
		}
	}
}

func TestAccept_NewInviteeSetsPasswordGrantsMembershipAndSignsIn(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyMinLength, "14"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}
	email := f.newEmail()
	userID, token := f.invite(t, email)

	rec := f.doAccept(t, token, newPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["access_token"] == nil {
		t.Errorf("body = %v, want a signed-in session", body)
	}

	u, err := f.users.GetByID(t.Context(), userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if u.Status != user.StatusActive {
		t.Errorf("status = %q, want active", u.Status)
	}
	if ok, err := argon2id.ComparePasswordAndHash(newPassword, *u.PasswordHash); err != nil || !ok {
		t.Errorf("stored hash doesn't match the chosen password (err %v)", err)
	}
	if !f.isMember(t, userID) {
		t.Error("invitee isn't a member after accepting")
	}
	// A token-authenticated acceptance has no signed-in actor
	// (auth-internals.md §17), the same as password.reset_completed.
	if want := (auditEvent{"user.invite_accepted", userID, ""}); len(f.audit.events) == 0 || f.audit.events[len(f.audit.events)-1] != want {
		t.Errorf("audit events = %v, want %v last", f.audit.events, want)
	}

	if again := f.doAccept(t, token, newPassword); again.Code != http.StatusNotFound {
		t.Errorf("reuse status = %d, want 404", again.Code)
	}
}

func TestAccept_WeakPasswordReturns422AndKeepsTheInvite(t *testing.T) {
	f := newFixture(t)
	userID, token := f.invite(t, f.newEmail())

	rec := f.doAccept(t, token, "short")
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "auth.password_too_weak" {
		t.Fatalf("status = %d, body = %s, want 422 auth.password_too_weak", rec.Code, rec.Body.String())
	}
	if got := minLengthDetail(t, rec); got != float64(password.Global.MinLength) {
		t.Errorf("details.min_length = %v, want %d", got, password.Global.MinLength)
	}
	if f.isMember(t, userID) {
		t.Error("membership granted despite a rejected password")
	}
	if retry := f.doAccept(t, token, newPassword); retry.Code != http.StatusOK {
		t.Errorf("retry status = %d, want 200", retry.Code)
	}
}

func TestAccept_ExistingAccountGetsMembershipButNoSession(t *testing.T) {
	f := newFixture(t)
	email := f.newEmail()
	userID, token := f.invite(t, email)
	existingHash, err := argon2id.CreateHash("their existing passphrase", password.ArgonParams)
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'active', password_hash = $2 WHERE id = $1`, userID, existingHash); err != nil {
		t.Fatalf("give invitee an existing password: %v", err)
	}

	if info := decodeBody(t, f.doInfo(t, f.tenantSlug, token)); info["password_required"] != false {
		t.Errorf("info password_required = %v, want false", info["password_required"])
	}

	rec := f.doAccept(t, token, "")
	if rec.Code != http.StatusOK || decodeBody(t, rec)["login_required"] != true {
		t.Fatalf("status = %d, body = %s, want 200 login_required", rec.Code, rec.Body.String())
	}
	if !f.isMember(t, userID) {
		t.Error("existing account isn't a member after accepting")
	}
	u, err := f.users.GetByID(t.Context(), userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if *u.PasswordHash != existingHash {
		t.Error("existing password was changed")
	}
	var sessions int
	if err := f.conn.QueryRow(`SELECT count(*) FROM system.sessions WHERE user_id = $1`, userID).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Errorf("sessions = %d, want 0", sessions)
	}
}

func TestAccept_SuspendedInviteeIsNotReactivated(t *testing.T) {
	f := newFixture(t)
	userID, token := f.invite(t, f.newEmail())
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'suspended' WHERE id = $1`, userID); err != nil {
		t.Fatalf("suspend invitee: %v", err)
	}

	rec := f.doAccept(t, token, newPassword)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "invite_conflict" {
		t.Fatalf("status = %d, body = %s, want 409 invite_conflict", rec.Code, rec.Body.String())
	}
	u, err := f.users.GetByID(t.Context(), userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if u.Status != user.StatusSuspended || u.PasswordHash != nil {
		t.Errorf("status = %q, password set = %v, want suspended with no password", u.Status, u.PasswordHash != nil)
	}
	if f.isMember(t, userID) {
		t.Error("membership granted to a suspended invitee")
	}
}

func minLengthDetail(t *testing.T, rec *httptest.ResponseRecorder) any {
	t.Helper()
	e, _ := decodeBody(t, rec)["error"].(map[string]any)
	details, _ := e["details"].(map[string]any)
	return details["min_length"]
}

func TestInfoAndAccept_UseTheTenantsMinimumLength(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyMinLength, "20"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}
	_, token := f.invite(t, f.newEmail())

	info := f.doInfo(t, f.tenantSlug, token)
	if got := decodeBody(t, info)["password_min_length"]; got != float64(20) {
		t.Errorf("info password_min_length = %v, want the tenant's 20", got)
	}

	rec := f.doAccept(t, token, "sixteen chars ok")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s, want 422 for a password under the tenant's minimum", rec.Code, rec.Body.String())
	}
	if got := minLengthDetail(t, rec); got != float64(20) {
		t.Errorf("details.min_length = %v, want 20", got)
	}
}
