package passwordreset

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func (f *fixture) activate(t *testing.T, slug string) {
	t.Helper()
	if _, err := f.tenants.UpdateStatus(t.Context(), slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate tenant %s: %v", slug, err)
	}
}

// addMembership creates a second active tenant where the fixture's user
// holds the admin role.
func (f *fixture) addMembership(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	roles := role.NewStore(f.conn)
	slug := fmt.Sprintf("pwresettwo%d", time.Now().UnixNano())
	tt, err := f.tenants.CreateTenant(ctx, slug, "Second Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
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
	f.activate(t, slug)
}

func TestRequest_NoTenantNamed_OneTenantEmailsItsLink(t *testing.T) {
	f := newFixture(t)
	f.activate(t, f.tenantSlug)

	if rec := f.doRequest(t, f.email, ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	f.mailer.waitSent(t)
	if sent := f.mailer.lastReset(t); sent.tenant != f.tenantSlug {
		t.Errorf("sent tenant = %q, want the account's only tenant %q", sent.tenant, f.tenantSlug)
	}
}

func TestRequest_NoTenantNamed_SeveralTenantsEmailATenantlessLinkThatResets(t *testing.T) {
	f := newFixture(t)
	f.activate(t, f.tenantSlug)
	f.addMembership(t)

	if rec := f.doRequest(t, f.email, ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	f.mailer.waitSent(t)
	sent := f.mailer.lastReset(t)
	if sent.tenant != "" {
		t.Errorf("sent tenant = %q, want none for a several-tenant account", sent.tenant)
	}

	rec := f.doConfirmIn(t, "", sent.rawToken, newPassword)
	if rec.Code != http.StatusOK || decodeBody(t, rec)["login_required"] != true {
		t.Fatalf("confirm status = %d, body = %s, want 200 login_required", rec.Code, rec.Body.String())
	}
	if !f.passwordMatches(t, newPassword) {
		t.Error("the tenantless link didn't set the new password")
	}
}

func TestRequest_NoTenantNamed_NoActiveTenantIssuesNothing(t *testing.T) {
	f := newFixture(t)

	if rec := f.doRequest(t, f.email, ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	f.mailer.assertNoneSent(t)
}
