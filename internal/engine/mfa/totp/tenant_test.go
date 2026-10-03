package totp

import (
	"errors"
	"testing"
	"time"

	pquernatotp "github.com/pquerna/otp/totp"

	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/mfatest"
)

func TestVerify_RejectsForeignAndTenantProofForPlatformRemoval(t *testing.T) {
	e := openTestEnv(t)
	userID := e.createUser(t)
	other := mfatest.NewTenant(t, e.conn)
	mfatest.AddMember(t, e.conn, other, userID)
	credentialID, secret := seedFactor(t, e, userID)
	if _, err := e.conn.ExecContext(t.Context(), `UPDATE system.user_mfa SET tenant_id=$1 WHERE id=$2`, e.tenant.ID, credentialID); err != nil {
		t.Fatal(err)
	}

	code, err := pquernatotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	for _, scope := range []mfa.Scope{{TenantID: other.ID}, {TenantID: e.tenant.ID, PlatformOnly: true}} {
		if valid, _, err := e.service.Verify(t.Context(), userID, code, scope); err != nil || valid {
			t.Fatalf("foreign/platform-only verification = %v, %v", valid, err)
		}
	}

	if valid, id, err := e.service.Verify(t.Context(), userID, code, mfa.Scope{TenantID: e.tenant.ID}); err != nil || !valid || id != credentialID {
		t.Fatalf("own tenant verification = %v, %q, %v", valid, id, err)
	}
}

func TestPendingEnrollment_IsBoundToTenant(t *testing.T) {
	e := openTestEnv(t)
	userID := e.createUser(t)
	other := mfatest.NewTenant(t, e.conn)
	pending, err := e.service.BeginEnrollment(t.Context(), userID, "member@example.com", e.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}

	code, err := pquernatotp.GenerateCode(pending.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.service.CheckEnrollmentCode(t.Context(), userID, pending.ID, code, other.ID); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Fatalf("foreign confirm = %v", err)
	}

	if _, err := e.service.CheckEnrollmentCode(t.Context(), userID, pending.ID, code, e.tenant.ID); err != nil {
		t.Fatal(err)
	}

}
