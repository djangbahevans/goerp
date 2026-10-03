package loginflow

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/mfatest"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestLogin_ChallengeUsesAcceptedFactorsAndTenantReset(t *testing.T) {
	f := newFixture(t)
	other := mfatest.NewTenant(t, f.conn)
	mfatest.AddMember(t, f.conn, other, f.userID)
	roles := role.NewStore(f.conn)
	if err := roles.SeedBuiltinRoles(t.Context(), other.Slug); err != nil {
		t.Fatal(err)
	}

	roleID, err := roles.GetRoleByName(t.Context(), other.Slug, "user")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.conn.ExecContext(t.Context(), `INSERT INTO `+tenantschema.Name(other.Slug)+`.user_roles (user_id, role_id) VALUES ($1,$2)`, f.userID, roleID); err != nil {
		t.Fatal(err)
	}
	tenants := tenant.NewStore(f.conn)
	if _, err := tenants.UpdateStatus(t.Context(), other.Slug, tenant.StatusActive, nil); err != nil {
		t.Fatal(err)
	}

	otherHost := other.Slug + "." + testPlatformDomain
	if _, err := tenants.CreateDomain(t.Context(), other.ID, otherHost, tenant.DomainSubdomain, true); err != nil {
		t.Fatal(err)
	}

	if err := f.mfaStore.WithTx(t.Context(), func(tx *sql.Tx) error {
		_, err := f.mfaStore.InsertScopedTx(t.Context(), tx, f.userID, new(other.ID), mfa.CredentialTOTP, []byte("foreign"), nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	assertChallenge := func(slug, host string, want bool) {
		t.Helper()
		rec := f.doLogin(t, map[string]any{
			"email": fixtureEmail(f), "password": testPassword, "tenant": slug,
		}, map[string]string{"Host": host, "X-Client-Type": "cli"})
		if rec.Code != http.StatusOK {
			t.Fatalf("login %s = %d, %s", slug, rec.Code, rec.Body.String())
		}

		body := decodeBody(t, rec)
		if got := body["mfa_required"] == true; got != want {
			t.Fatalf("login %s challenge=%v, want %v: %v", slug, got, want, body)
		}

		if !want && body["access_token"] == nil {
			t.Fatalf("password-only login did not issue a session: %v", body)
		}
	}

	assertChallenge(f.tenantSlug, f.host, false)
	assertChallenge(other.Slug, otherHost, true)
	if _, err := f.mfaStore.Insert(t.Context(), f.userID, mfa.CredentialTOTP, []byte("platform"), nil); err != nil {
		t.Fatal(err)
	}

	if err := f.mfaStore.WithTx(t.Context(), func(tx *sql.Tx) error {
		return f.mfaStore.SetResetTx(t.Context(), tx, f.userID, f.tenantID, true)
	}); err != nil {
		t.Fatal(err)
	}

	assertChallenge(f.tenantSlug, f.host, false)
	assertChallenge(other.Slug, otherHost, true)
}
