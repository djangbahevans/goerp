package loginflow

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func (f *fixture) activateTenant(t *testing.T, slug string) {
	t.Helper()
	if _, err := tenant.NewStore(f.conn).UpdateStatus(t.Context(), slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate tenant %s: %v", slug, err)
	}
}

// addMembership creates a second active tenant where the fixture's user
// holds the admin role, and returns its slug.
func (f *fixture) addMembership(t *testing.T, name string) string {
	t.Helper()
	ctx := t.Context()
	tenants := tenant.NewStore(f.conn)
	roles := role.NewStore(f.conn)

	slug := fmt.Sprintf("loginflowtwo%d", time.Now().UnixNano())
	tt, err := tenants.CreateTenant(ctx, slug, name)
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
	domain := slug + "." + testPlatformDomain
	if _, err := tenants.CreateDomain(ctx, tt.ID, domain, tenant.DomainSubdomain, true); err != nil {
		t.Fatalf("CreateDomain() error: %v", err)
	}
	t.Cleanup(func() { _ = f.cache.Delete(context.Background(), tenantresolve.DomainCacheKey(domain)) })
	schema := tenantschema.Name(slug)
	if _, err := f.conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)) })
	if err := roles.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roles.BootstrapMembershipIndex(ctx); err != nil {
		t.Fatalf("BootstrapMembershipIndex() error: %v", err)
	}
	if err := roles.AttachMembershipTrigger(ctx, slug); err != nil {
		t.Fatalf("AttachMembershipTrigger() error: %v", err)
	}
	if err := roles.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := roles.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	if err := roles.AddMember(ctx, slug, f.userID); err != nil {
		t.Fatalf("AddMember() error: %v", err)
	}
	if err := roles.AssignRole(ctx, slug, f.userID, roleID, ""); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}
	f.activateTenant(t, slug)
	return slug
}

func (f *fixture) loginTenantless(t *testing.T, host string) *httptest.ResponseRecorder {
	t.Helper()
	return f.doLogin(t, map[string]any{"email": fixtureEmail(f), "password": testPassword}, map[string]string{"Host": host})
}

func (f *fixture) selectTenant(t *testing.T, host, token, slug string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(map[string]string{"selection_token": token, "tenant": slug})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/select-tenant", bytes.NewReader(b))
	req.Host = host
	req.RemoteAddr = f.remoteIP + ":54321"
	rec := httptest.NewRecorder()
	f.handler.ServeSelectTenant(rec, req)
	return rec
}

// tenantRequired returns the tenants and selection_token a 409
// tenant_required login answered with.
func tenantRequired(t *testing.T, rec *httptest.ResponseRecorder) (tenants []any, token string) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("login status = %d, body = %s, want 409", rec.Code, rec.Body.String())
	}
	errBody, _ := decodeBody(t, rec)["error"].(map[string]any)
	if errBody["code"] != "tenant_required" {
		t.Fatalf("error.code = %v, want tenant_required", errBody["code"])
	}
	details, _ := errBody["details"].(map[string]any)
	tenants, _ = details["tenants"].([]any)
	token, _ = details["selection_token"].(string)
	if token == "" {
		t.Fatalf("details = %v, want a selection_token", details)
	}
	return tenants, token
}

func TestServeHTTP_Tenantless_SingleTenantSignsInThatTenant(t *testing.T) {
	f := newFixture(t)
	f.activateTenant(t, f.tenantSlug)

	host, code := handoffFrom(t, f.loginTenantless(t, sharedHost))
	if host != f.host {
		t.Errorf("handoff host = %q, want %q", host, f.host)
	}
	if rec := f.exchange(t, f.host, code); rec.Code != http.StatusOK {
		t.Errorf("exchange status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_Tenantless_NoActiveMembershipIsInvalidCredentials(t *testing.T) {
	f := newFixture(t)

	rec := f.loginTenantless(t, sharedHost)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_Tenantless_WrongPasswordRevealsNoTenants(t *testing.T) {
	f := newFixture(t)
	f.activateTenant(t, f.tenantSlug)
	f.addMembership(t, "Second Co")

	rec := f.doLogin(t, map[string]any{"email": fixtureEmail(f), "password": "wrong"}, map[string]string{"Host": sharedHost})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s, want 401", rec.Code, rec.Body.String())
	}
}

func TestServeSelectTenant_SeveralTenantsPickOneOnce(t *testing.T) {
	f := newFixture(t)
	f.activateTenant(t, f.tenantSlug)
	second := f.addMembership(t, "Second Co")

	tenants, token := tenantRequired(t, f.loginTenantless(t, sharedHost))
	if len(tenants) != 2 {
		t.Fatalf("tenants = %v, want both memberships", tenants)
	}
	names := map[any]any{}
	for _, raw := range tenants {
		entry, _ := raw.(map[string]any)
		names[entry["slug"]] = entry["name"]
	}
	if names[f.tenantSlug] != "Login Flow Test Co" || names[second] != "Second Co" {
		t.Errorf("tenants = %v, want slug/name pairs for both", tenants)
	}

	host, code := handoffFrom(t, f.selectTenant(t, sharedHost, token, second))
	if host != second+"."+testPlatformDomain {
		t.Errorf("handoff host = %q, want the chosen tenant's", host)
	}
	if rec := f.exchange(t, host, code); rec.Code != http.StatusOK {
		t.Errorf("exchange status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}

	again := f.selectTenant(t, sharedHost, token, second)
	if again.Code != http.StatusUnauthorized {
		t.Fatalf("reused token status = %d, want 401", again.Code)
	}
	errBody, _ := decodeBody(t, again)["error"].(map[string]any)
	if errBody["code"] != "auth.selection_token_invalid" {
		t.Errorf("error.code = %v, want auth.selection_token_invalid", errBody["code"])
	}
}

func TestServeSelectTenant_AppliesThePickedTenantsPasswordPolicyResult(t *testing.T) {
	f := newFixture(t)
	f.activateTenant(t, f.tenantSlug)
	second := f.addMembership(t, "Second Co")
	// The fixture tenant asks for 16 characters; the second keeps the
	// platform minimum.
	f.useShortPassword(t, "nudge", "14")

	for _, tc := range []struct {
		slug, host  string
		recommended bool
	}{
		{second, second + "." + testPlatformDomain, false},
		{f.tenantSlug, f.host, true},
	} {
		rec := f.doLogin(t, map[string]any{"email": fixtureEmail(f), "password": shortPassword}, map[string]string{"Host": sharedHost})
		_, token := tenantRequired(t, rec)
		host, code := handoffFrom(t, f.selectTenant(t, sharedHost, token, tc.slug))
		if host != tc.host {
			t.Fatalf("handoff host = %q, want %q", host, tc.host)
		}
		exchanged := f.exchange(t, host, code)
		if exchanged.Code != http.StatusOK {
			t.Fatalf("exchange status = %d, body = %s, want 200", exchanged.Code, exchanged.Body.String())
		}
		if got := decodeBody(t, exchanged)["password_update_recommended"] == true; got != tc.recommended {
			t.Errorf("tenant %s: password_update_recommended = %v, want %v", tc.slug, got, tc.recommended)
		}
	}
}

func TestServeSelectTenant_MembershipRevokedSinceLoginIsForbidden(t *testing.T) {
	f := newFixture(t)
	f.activateTenant(t, f.tenantSlug)
	second := f.addMembership(t, "Second Co")
	_, token := tenantRequired(t, f.loginTenantless(t, sharedHost))

	if _, err := f.conn.Exec(fmt.Sprintf("DELETE FROM %s.user_roles WHERE user_id = $1", tenantschema.Name(second)), f.userID); err != nil {
		t.Fatalf("revoke membership: %v", err)
	}
	rec := f.selectTenant(t, sharedHost, token, second)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
	errBody, _ := decodeBody(t, rec)["error"].(map[string]any)
	if errBody["code"] != "tenant_membership_required" {
		t.Errorf("error.code = %v, want tenant_membership_required", errBody["code"])
	}
}

func TestServeSelectTenant_OnTheChosenTenantsHostSetsTheSession(t *testing.T) {
	f := newFixture(t)
	f.activateTenant(t, f.tenantSlug)
	f.addMembership(t, "Second Co")
	_, token := tenantRequired(t, f.loginTenantless(t, sharedHost))

	rec := f.selectTenant(t, f.host, token, f.tenantSlug)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("select-tenant on the tenant's host set no cookies, want a session")
	}
}
