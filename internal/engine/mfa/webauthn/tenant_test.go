package webauthn

import (
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/mfatest"
)

func TestCeremonies_RejectOtherTenantForSameAccount(t *testing.T) {
	e := openTestEnv(t)
	userID := e.createUser(t)
	other := mfatest.NewTenant(t, e.conn)
	mfatest.AddMember(t, e.conn, other, userID)
	register(t, e, userID)

	_, registration, err := e.service.BeginRegistration(t.Context(), userID, "user@example.com", mfa.Scope{TenantID: e.tenant.ID})
	if err != nil {
		t.Fatal(err)
	}

	_, err = e.service.FinishRegistration(t.Context(), userID, registration, "user@example.com", nil, nil, mfa.Scope{TenantID: other.ID}, e.sessionID(t, userID))
	if !errors.Is(err, ErrCeremonyUserMismatch) {
		t.Fatalf("foreign registration completion = %v", err)
	}

	_, login, err := e.service.BeginLogin(t.Context(), userID, "user@example.com", mfa.Scope{TenantID: e.tenant.ID})
	if err != nil {
		t.Fatal(err)
	}

	_, err = e.service.FinishLogin(t.Context(), userID, login, "user@example.com", nil, mfa.Scope{TenantID: other.ID})
	if !errors.Is(err, ErrCeremonyUserMismatch) {
		t.Fatalf("foreign login completion = %v", err)
	}

	if _, err := e.conn.ExecContext(t.Context(), `UPDATE system.user_mfa SET tenant_id=$2 WHERE user_id=$1 AND type='webauthn'`, userID, e.tenant.ID); err != nil {
		t.Fatal(err)
	}

	_, _, err = e.service.BeginLogin(t.Context(), userID, "user@example.com", mfa.Scope{TenantID: other.ID})
	if !errors.Is(err, ErrNoEnrolledCredentials) {
		t.Fatalf("foreign tenant accepted scoped authenticator: %v", err)
	}

	_, _, err = e.service.BeginLogin(t.Context(), userID, "user@example.com", mfa.Scope{TenantID: e.tenant.ID})
	if err != nil {
		t.Fatalf("own tenant rejected scoped authenticator: %v", err)
	}
}
