package authregister

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
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/handoff"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantprovision "github.com/djangbahevans/goerp/internal/engine/tenant/provision"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
	goodPassword     = "a brand new long passphrase"
)

// fakeProvisioner stands in for ProvisionTenantWorkflow: it creates the
// tenant, its schema and roles, and grants the registrant admin, the same
// end state the real workflow leaves (tenant/provision's own tests cover
// the workflow itself). err, when set, is returned instead.
type fakeProvisioner struct {
	conn    *sql.DB
	tenants *tenant.Store
	roles   *role.Store
	err     error

	mu    sync.Mutex
	calls []provisionCall
}

type provisionCall struct{ slug, name, userID string }

func (p *fakeProvisioner) ProvisionForRegistration(ctx context.Context, slug, name, userID string) error {
	p.mu.Lock()
	p.calls = append(p.calls, provisionCall{slug, name, userID})
	p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if _, err := p.tenants.CreateTenant(ctx, slug, name); err != nil {
		return err
	}
	if _, err := p.conn.ExecContext(ctx, "CREATE SCHEMA "+tenantschema.Name(slug)); err != nil {
		return err
	}
	if err := p.roles.Bootstrap(ctx, slug); err != nil {
		return err
	}
	if err := p.roles.SeedBuiltinRoles(ctx, slug); err != nil {
		return err
	}
	roleID, err := p.roles.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		return err
	}
	if err := p.roles.AssignRole(ctx, slug, userID, roleID, ""); err != nil {
		return err
	}
	_, err = p.tenants.UpdateStatus(ctx, slug, tenant.StatusActive, nil)
	return err
}

type fakeMailer struct {
	mu   sync.Mutex
	sent map[string]string
	done chan struct{}
}

func (m *fakeMailer) SendVerifyEmail(_ context.Context, email, _, rawToken string) error {
	m.mu.Lock()
	m.sent[email] = rawToken
	m.mu.Unlock()
	m.done <- struct{}{}
	return nil
}

type fixture struct {
	conn        *sql.DB
	users       *user.Store
	tenants     *tenant.Store
	roles       *role.Store
	issuer      *authtoken.Issuer
	provisioner *fakeProvisioner
	mailer      *fakeMailer
	handoffs    *handoff.Store
}

const testPlatformDomain = "register.test"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := t.Context()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockSigningKeyTable(t, conn)

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
	roleStore := role.NewStore(conn)

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })
	billingStore := billing.NewStore(conn)
	if err := billingStore.Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}
	resolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)

	return &fixture{
		conn:        conn,
		users:       userStore,
		tenants:     tenantStore,
		roles:       roleStore,
		issuer:      authtoken.NewIssuer(&keySet.Active, tenantStore, roleStore, sessionStore),
		provisioner: &fakeProvisioner{conn: conn, tenants: tenantStore, roles: roleStore},
		mailer:      &fakeMailer{sent: map[string]string{}, done: make(chan struct{}, 4)},
		handoffs:    handoff.NewStore(cacheClient, resolver, testPlatformDomain),
	}
}

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

func (f *fixture) handlers(enabled bool, policy string) *Handlers {
	return NewHandlers(Config{Enabled: enabled, VerificationPolicy: policy, ProvisionTimeout: 10 * time.Second}, f.users, f.tenants, f.provisioner, password.NewHasher(256, time.Second), f.issuer, f.mailer, f.handoffs)
}

// registration returns a unique company name and email, cleaning up the
// tenant, schema and account it produces.
func (f *fixture) registration(t *testing.T) (company, slug, email string) {
	t.Helper()
	n := time.Now().UnixNano()
	company = fmt.Sprintf("Register Test %d", n)
	slug = DeriveSlug(company)
	email = fmt.Sprintf("founder%d@example.com", n)
	t.Cleanup(func() {
		_, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE user_id IN (SELECT id FROM system.users WHERE email = $1)`, email)
		_, _ = f.conn.Exec(`DELETE FROM system.users WHERE email = $1`, email)
		_, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE slug = $1`, slug)
		_ = tenantschema.Drop(context.Background(), f.conn, slug)
	})
	return company, slug, email
}

func doRegister(t *testing.T, h *Handlers, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(b))
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("X-Client-Type", "cli")
	rec := httptest.NewRecorder()
	h.Register(rec, req)
	return rec
}

func doCheckSlug(t *testing.T, h *Handlers, slug string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.CheckSlug(rec, httptest.NewRequest(http.MethodGet, "/auth/check-slug?slug="+slug, nil))
	return rec
}

func body(company, email string) map[string]string {
	return map[string]string{"name": "Kwame Mensah", "email": email, "password": goodPassword, "company_name": company}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return v
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	e, _ := decode(t, rec)["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestDisabled_BothEndpointsReturn404(t *testing.T) {
	f := newFixture(t)
	h := f.handlers(false, VerificationOff)
	company, slug, email := f.registration(t)

	if rec := doRegister(t, h, body(company, email)); rec.Code != http.StatusNotFound {
		t.Errorf("register status = %d, want 404", rec.Code)
	}
	if rec := doCheckSlug(t, h, slug); rec.Code != http.StatusNotFound {
		t.Errorf("check-slug status = %d, want 404", rec.Code)
	}
	if len(f.provisioner.calls) != 0 {
		t.Error("provisioning ran with registration disabled")
	}
}

func TestCheckSlug(t *testing.T) {
	f := newFixture(t)
	h := f.handlers(true, VerificationOff)
	existing := fmt.Sprintf("taken%d", time.Now().UnixNano())
	if _, err := f.tenants.CreateTenant(t.Context(), existing, "Taken Co"); err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE slug = $1`, existing) })

	for slug, want := range map[string]bool{
		fmt.Sprintf("free%d", time.Now().UnixNano()): true,
		existing: false,
		"ab":     false,
		"Upper":  false,
	} {
		rec := doCheckSlug(t, h, slug)
		if rec.Code != http.StatusOK || decode(t, rec)["available"] != want {
			t.Errorf("check-slug(%q) = %d %s, want available=%v", slug, rec.Code, rec.Body.String(), want)
		}
	}
}

func TestRegister_BrowserOnSharedHostGetsHandoff(t *testing.T) {
	f := newFixture(t)
	h := f.handlers(true, VerificationOff)
	company, slug, email := f.registration(t)

	b, err := json.Marshal(body(company, email))
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(b))
	req.Host = "app." + testPlatformDomain
	req.RemoteAddr = "203.0.113.7:54321"
	rec := httptest.NewRecorder()
	h.Register(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s, want 201", rec.Code, rec.Body.String())
	}
	resp := decode(t, rec)
	ho, _ := resp["handoff"].(map[string]any)
	if resp["tenant_slug"] != slug || ho == nil || ho["host"] != slug+"."+testPlatformDomain || ho["code"] == "" {
		t.Errorf("body = %v, want tenant_slug %q and a handoff to %s.%s", resp, slug, slug, testPlatformDomain)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("cookies = %v, want none on the shared host", rec.Result().Cookies())
	}
}

func TestRegister_VerificationOffSignsInWithANewTenant(t *testing.T) {
	f := newFixture(t)
	h := f.handlers(true, VerificationOff)
	company, slug, email := f.registration(t)

	rec := doRegister(t, h, body(company, email))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s, want 201", rec.Code, rec.Body.String())
	}
	resp := decode(t, rec)
	if resp["tenant_slug"] != slug || resp["access_token"] == nil || resp["expires_in"] == nil {
		t.Errorf("body = %v, want tenant_slug %q and tokens", resp, slug)
	}

	u, err := f.users.GetByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("GetByEmail() error: %v", err)
	}
	if u.Status != user.StatusActive || u.PasswordHash == nil {
		t.Errorf("status = %q, password set = %v, want active with a password", u.Status, u.PasswordHash != nil)
	}
	if p, err := f.users.GetProfile(t.Context(), u.ID); err != nil || p.Name != "Kwame Mensah" {
		t.Errorf("profile = %+v, %v, want name Kwame Mensah", p, err)
	}
	if calls := f.provisioner.calls; len(calls) != 1 || calls[0] != (provisionCall{slug, company, u.ID}) {
		t.Errorf("provision calls = %+v, want one for %s/%s/%s", calls, slug, company, u.ID)
	}
	roles, err := f.roles.RoleNamesForUser(t.Context(), slug, u.ID)
	if err != nil || len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("roles = %v, %v, want [admin]", roles, err)
	}
}

func TestRegister_VerificationRequiredOrTenantChoiceReturns202(t *testing.T) {
	for _, policy := range []string{VerificationRequired, VerificationTenantChoice} {
		t.Run(policy, func(t *testing.T) {
			f := newFixture(t)
			h := f.handlers(true, policy)
			company, _, email := f.registration(t)

			rec := doRegister(t, h, body(company, email))
			if resp := decode(t, rec); resp["tenant_slug"] != DeriveSlug(company) {
				t.Errorf("tenant_slug = %v, want %q", resp["tenant_slug"], DeriveSlug(company))
			}
			if rec.Code != http.StatusAccepted || decode(t, rec)["requires_email_verification"] != true {
				t.Fatalf("status = %d, body = %s, want 202 requires_email_verification", rec.Code, rec.Body.String())
			}
			u, err := f.users.GetByEmail(t.Context(), email)
			if err != nil {
				t.Fatalf("GetByEmail() error: %v", err)
			}
			if u.Status != user.StatusPendingVerification {
				t.Errorf("status = %q, want pending_verification", u.Status)
			}

			select {
			case <-f.mailer.done:
			case <-time.After(2 * time.Second):
				t.Fatal("no verification email sent")
			}
			var stored string
			var expiry time.Time
			if err := f.conn.QueryRow(`SELECT email_verify_token, email_verify_expiry FROM system.users WHERE id = $1`, u.ID).Scan(&stored, &expiry); err != nil {
				t.Fatalf("read verify token: %v", err)
			}
			f.mailer.mu.Lock()
			raw := f.mailer.sent[email]
			f.mailer.mu.Unlock()
			if sum := sha256.Sum256([]byte(raw)); stored != hex.EncodeToString(sum[:]) {
				t.Error("stored token is not the SHA-256 of the emailed token")
			}
			if until := time.Until(expiry); until < 23*time.Hour || until > 24*time.Hour {
				t.Errorf("expiry in %v, want ~24h", until)
			}
		})
	}
}

func TestRegister_ValidationFailuresReturn422WithFieldDetails(t *testing.T) {
	f := newFixture(t)
	h := f.handlers(true, VerificationOff)

	rec := doRegister(t, h, map[string]string{"name": "", "email": "not-an-email", "password": "short", "company_name": "AB"})
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "validation_failed" {
		t.Fatalf("status = %d, body = %s, want 422 validation_failed", rec.Code, rec.Body.String())
	}
	details, _ := decode(t, rec)["error"].(map[string]any)["details"].(map[string]any)
	for _, field := range []string{"name", "email", "password", "company_name"} {
		if _, ok := details[field]; !ok {
			t.Errorf("details missing %q: %v", field, details)
		}
	}
	if len(f.provisioner.calls) != 0 {
		t.Error("provisioning ran for an invalid registration")
	}
}

func TestRegister_DuplicateEmailAndSlugReturn409(t *testing.T) {
	f := newFixture(t)
	h := f.handlers(true, VerificationOff)
	company, _, email := f.registration(t)
	if rec := doRegister(t, h, body(company, email)); rec.Code != http.StatusCreated {
		t.Fatalf("first registration status = %d, body = %s", rec.Code, rec.Body.String())
	}

	otherCompany, _, otherEmail := f.registration(t)
	if rec := doRegister(t, h, body(otherCompany, email)); rec.Code != http.StatusConflict || errorCode(t, rec) != "auth.email_already_exists" {
		t.Errorf("duplicate email = %d %s, want 409 auth.email_already_exists", rec.Code, rec.Body.String())
	}
	if rec := doRegister(t, h, body(company, otherEmail)); rec.Code != http.StatusConflict || errorCode(t, rec) != "tenant.slug_taken" {
		t.Errorf("duplicate slug = %d %s, want 409 tenant.slug_taken", rec.Code, rec.Body.String())
	}
}

func TestRegister_ProvisioningFailureRemovesTheAccount(t *testing.T) {
	for name, c := range map[string]struct {
		err        error
		wantStatus int
	}{
		"slug taken during provisioning": {tenantprovision.ErrSlugTaken, http.StatusConflict},
		"provisioning failed":            {errors.New("temporal down"), http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.provisioner.err = c.err
			h := f.handlers(true, VerificationOff)
			company, _, email := f.registration(t)

			if rec := doRegister(t, h, body(company, email)); rec.Code != c.wantStatus {
				t.Fatalf("status = %d, body = %s, want %d", rec.Code, rec.Body.String(), c.wantStatus)
			}
			if _, err := f.users.GetByEmail(t.Context(), email); !errors.Is(err, user.ErrUserNotFound) {
				t.Errorf("GetByEmail() error = %v, want ErrUserNotFound — the account should be removed so the email can register again", err)
			}
		})
	}
}

func TestRegister_ProvisioningTimeoutKeepsTheAccount(t *testing.T) {
	for _, policy := range []string{VerificationOff, VerificationRequired} {
		t.Run(policy, func(t *testing.T) {
			f := newFixture(t)
			f.provisioner.err = tenantprovision.ErrProvisioningPending
			h := f.handlers(true, policy)
			company, _, email := f.registration(t)

			rec := doRegister(t, h, body(company, email))
			resp := decode(t, rec)
			wantVerify := policy == VerificationRequired
			if rec.Code != http.StatusAccepted || resp["provisioning_pending"] != true || resp["requires_email_verification"] != wantVerify {
				t.Fatalf("status = %d, body = %s, want 202 provisioning_pending with requires_email_verification=%v", rec.Code, rec.Body.String(), wantVerify)
			}
			if _, err := f.users.GetByEmail(t.Context(), email); err != nil {
				t.Errorf("GetByEmail() error = %v, want the account kept for the still-running workflow", err)
			}
		})
	}
}

func TestReservedSlugs_ReadAsTaken(t *testing.T) {
	f := newFixture(t)
	f.tenants.AddReservedSlugs("wiki")
	h := f.handlers(true, VerificationOff)

	for _, slug := range []string{"app", "storage", "wiki"} {
		if rec := doCheckSlug(t, h, slug); decode(t, rec)["available"] != false {
			t.Errorf("check-slug(%q) = %s, want available=false", slug, rec.Body.String())
		}
	}

	_, _, email := f.registration(t)
	rec := doRegister(t, h, body("App", email))
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "tenant.slug_taken" {
		t.Errorf("register as App = %d %s, want 409 tenant.slug_taken", rec.Code, rec.Body.String())
	}
	if len(f.provisioner.calls) != 0 {
		t.Error("provisioning ran for a reserved slug")
	}
}
