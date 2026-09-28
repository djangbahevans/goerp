package loginflow

import (
	"net/http"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/auth/ipallowlist"
)

func (f *fixture) setAllowlist(t *testing.T, value string) {
	t.Helper()
	if err := f.config.Set(t.Context(), f.tenantID, ipallowlist.Key, value); err != nil {
		t.Fatalf("set ip allowlist: %v", err)
	}
}

func (f *fixture) loginCLI(t *testing.T) int {
	t.Helper()
	return f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, map[string]string{"X-Client-Type": "cli"}).Code
}

func TestServeHTTP_IPAllowlist_RejectsAnAddressOutsideIt(t *testing.T) {
	f := newFixture(t)
	f.setAllowlist(t, "203.0.113.0/24")

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": testPassword, "tenant": f.tenantSlug,
	}, map[string]string{"X-Client-Type": "cli"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
	if got := errorCode(t, rec); got != "ip_not_allowed" {
		t.Errorf("error code = %q, want ip_not_allowed", got)
	}

	f.setAllowlist(t, "")
	if code := f.loginCLI(t); code != http.StatusOK {
		t.Fatalf("status after clearing the allowlist = %d, want 200", code)
	}
}

func TestServeHTTP_IPAllowlist_AdmitsAnAddressInsideIt(t *testing.T) {
	f := newFixture(t)
	f.setAllowlist(t, "203.0.113.0/24, "+f.remoteIP+"/32")

	if code := f.loginCLI(t); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
}

func TestServeHTTP_IPAllowlist_WrongPasswordStillLooksLikeWrongPassword(t *testing.T) {
	f := newFixture(t)
	f.setAllowlist(t, "203.0.113.0/24")

	rec := f.doLogin(t, map[string]any{
		"email": fixtureEmail(f), "password": "not the password", "tenant": f.tenantSlug,
	}, map[string]string{"X-Client-Type": "cli"})
	if got := errorCode(t, rec); got != "invalid_credentials" {
		t.Errorf("error code = %q, want invalid_credentials, not a hint about the allowlist", got)
	}
}

func TestServeHTTP_IPAllowlist_UnreadableListFailsClosed(t *testing.T) {
	f := newFixture(t)
	f.setAllowlist(t, "not-a-cidr")

	if code := f.loginCLI(t); code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", code)
	}
}

func TestServeHandoff_IPAllowlist_ChecksTheExchangingAddress(t *testing.T) {
	f := newFixture(t)
	_, code := handoffFrom(t, f.loginOnSharedHost(t))
	f.setAllowlist(t, "203.0.113.0/24")

	rec := f.exchange(t, f.host, code)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("exchange status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
}
