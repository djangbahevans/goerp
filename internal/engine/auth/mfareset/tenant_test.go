package mfareset

import (
	"database/sql"
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/mfatest"
)

func TestReset_PreservesOtherTenantsFactorsAndSessions(t *testing.T) {
	f := newFixture(t)
	caller := f.createCallerWithPassword(t)
	callerToken := f.issueAccessToken(t, caller)
	target, email := f.createUserWithRole(t, "user")
	other := mfatest.NewTenant(t, f.conn)
	mfatest.AddMember(t, f.conn, other, target)

	platform, err := f.mfa.Insert(t.Context(), target, mfa.CredentialTOTP, []byte("platform"), nil)
	if err != nil {
		t.Fatal(err)
	}

	var own, foreign *mfa.Credential
	err = f.mfa.WithTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		own, err = f.mfa.InsertScopedTx(t.Context(), tx, target, new(f.tenantID), mfa.CredentialTOTP, []byte("own"), nil)
		if err != nil {
			return err
		}

		foreign, err = f.mfa.InsertScopedTx(t.Context(), tx, target, new(other.ID), mfa.CredentialTOTP, []byte("foreign"), nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: target, TenantSlug: f.tenantSlug}); err != nil {
		t.Fatal(err)
	}

	otherSession := uuid.NewV7().String()
	if err := f.sessions.Insert(t.Context(), session.Row{
		ID: otherSession, UserID: target, TenantID: other.ID, DeviceID: uuid.NewV7().String(),
		RefreshHash: otherSession, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	rec := f.doReset(t, callerToken, target, map[string]any{"password": testCallerPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("reset = %d, %s", rec.Code, rec.Body.String())
	}

	accepted, err := f.mfa.ListAccepted(t.Context(), target, mfa.Scope{TenantID: f.tenantID})
	if err != nil || len(accepted) != 0 {
		t.Fatalf("reset tenant accepts %v, %v", accepted, err)
	}

	accepted, err = f.mfa.ListAccepted(t.Context(), target, mfa.Scope{TenantID: other.ID})
	if err != nil || len(accepted) != 2 {
		t.Fatalf("other tenant accepts %v, %v", accepted, err)
	}

	for _, c := range accepted {
		if c.ID != platform.ID && c.ID != foreign.ID {
			t.Errorf("unexpected factor %s in other tenant", c.ID)
		}
	}

	var ownRevoked, otherSessionRevoked sql.NullTime
	if err := f.conn.QueryRowContext(t.Context(), `SELECT revoked_at FROM system.user_mfa WHERE id=$1`, own.ID).Scan(&ownRevoked); err != nil {
		t.Fatal(err)
	}

	if err := f.conn.QueryRowContext(t.Context(), `SELECT revoked_at FROM system.sessions WHERE id=$1`, otherSession).Scan(&otherSessionRevoked); err != nil {
		t.Fatal(err)
	}

	if !ownRevoked.Valid || otherSessionRevoked.Valid {
		t.Fatalf("own factor revoked=%v, other session revoked=%v", ownRevoked.Valid, otherSessionRevoked.Valid)
	}

	if !f.mailer.sentTo(email) || len(f.mailer.tenants) != 1 || f.mailer.tenants[0] != "MFA Reset Test Co" {
		t.Fatalf("reset notification = %v, %v", f.mailer.emails, f.mailer.tenants)
	}
}
