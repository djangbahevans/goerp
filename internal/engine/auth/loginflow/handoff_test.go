package loginflow

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/handoff"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

// sharedHost resolves to no tenant, like the shared-domain login host.
const sharedHost = "app.loginflow.test"

func (f *fixture) loginOnSharedHost(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, map[string]string{"Host": sharedHost})
}

func (f *fixture) exchange(t *testing.T, host, code string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/handoff", bytes.NewReader(b))
	req.Host = host
	req.RemoteAddr = f.remoteIP + ":54321"
	rec := httptest.NewRecorder()
	f.handler.ServeHandoff(rec, req)
	return rec
}

// handoffFrom returns the handoff a shared-host login answered with.
func handoffFrom(t *testing.T, rec *httptest.ResponseRecorder) (host, code string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	h, ok := decodeBody(t, rec)["handoff"].(map[string]any)
	if !ok {
		t.Fatalf("login body = %s, want a handoff", rec.Body.String())
	}
	host, _ = h["host"].(string)
	code, _ = h["code"].(string)
	if code == "" {
		t.Fatalf("handoff = %v, want a code", h)
	}
	return host, code
}

func TestServeHTTP_SharedHost_ReturnsHandoffWithoutSession(t *testing.T) {
	f := newFixture(t)

	rec := f.loginOnSharedHost(t)

	host, _ := handoffFrom(t, rec)
	if host != f.host {
		t.Errorf("handoff host = %q, want the tenant's default domain %q", host, f.host)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("cookies = %v, want none on the shared host", rec.Result().Cookies())
	}
}

func TestServeHandoff_IssuesSessionOnTenantHost_Once(t *testing.T) {
	f := newFixture(t)
	_, code := handoffFrom(t, f.loginOnSharedHost(t))

	rec := f.exchange(t, f.host, code)
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	var sawAccess bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-access_token" && c.Value != "" {
			sawAccess = true
		}
	}
	if !sawAccess {
		t.Errorf("cookies = %v, want __Host-access_token set on the tenant host", rec.Result().Cookies())
	}

	again := f.exchange(t, f.host, code)
	if again.Code != http.StatusUnauthorized {
		t.Fatalf("second exchange status = %d, body = %s, want 401", again.Code, again.Body.String())
	}
	if got := decodeBody(t, again)["error"].(map[string]any)["code"]; got != "auth.handoff_code_invalid" {
		t.Errorf("second exchange code = %v, want auth.handoff_code_invalid", got)
	}
}

func TestServeHandoff_RejectsCodeBoundToAnotherTenant(t *testing.T) {
	f := newFixture(t)
	// One fixture per test: each holds the package's signing-key advisory
	// lock until cleanup, so a second fixture here would block forever.
	resp, err := f.handler.handoffs.Issue(t.Context(), handoff.Grant{UserID: f.userID, TenantID: uuid.New().String()}, "elsewhere")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	rec := f.exchange(t, f.host, resp.Code)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401 for a code bound to another tenant", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("cookies = %v, want none", rec.Result().Cookies())
	}
}

func TestServeHandoff_MFAEnrolled_ChallengesOnTenantHost(t *testing.T) {
	f := newFixture(t)
	if _, err := f.mfaStore.Insert(t.Context(), f.userID, mfa.CredentialTOTP, []byte("x"), nil); err != nil {
		t.Fatalf("Insert() mfa credential error: %v", err)
	}
	_, code := handoffFrom(t, f.loginOnSharedHost(t))

	rec := f.exchange(t, f.host, code)
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["mfa_required"] != true || body["mfa_token"] == nil {
		t.Errorf("exchange body = %v, want mfa_required with an mfa_token", body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("cookies = %v, want none before the MFA challenge", rec.Result().Cookies())
	}
}

func TestServeHTTP_SharedHost_NonBrowserStillGetsTokens(t *testing.T) {
	f := newFixture(t)

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, map[string]string{"Host": sharedHost, "X-Client-Type": "cli"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["handoff"] != nil || body["access_token"] == nil {
		t.Errorf("body = %v, want tokens in the body and no handoff", body)
	}
}
