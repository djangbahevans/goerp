package mfaenroll

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/mfa/mfatest"
)

func TestConfirm_AfterResetCreatesTenantFactorAndScopedRecoveryCodes(t *testing.T) {
	f := newFixture(t)
	f.enrollFirst(t, f.login(t, f.userID))
	other := mfatest.NewTenant(t, f.conn)
	mfatest.AddMember(t, f.conn, other, f.userID)
	if err := f.handlers.mfa.WithTx(t.Context(), func(tx *sql.Tx) error {
		return f.handlers.mfa.SetResetTx(t.Context(), tx, f.userID, f.tenantID, true)
	}); err != nil {
		t.Fatal(err)
	}
	f.requireMFA(t)
	token := f.login(t, f.userID)
	if required, err := f.checker.MFASetupRequired(t.Context(), f.tenantID, f.authContext(t, token)); err != nil || !required {
		t.Fatalf("setup required = %v, %v", required, err)
	}

	id, secret := f.begin(t, token)
	status, body := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", token, map[string]any{"enrollment_id": id, "code": currentCode(t, secret)})
	if status != http.StatusOK {
		t.Fatalf("confirm = %d, %v", status, body)
	}

	if codes, _ := body["recovery_codes"].([]any); len(codes) != 10 {
		t.Fatalf("recovery codes = %v", body)
	}

	var scopedFactors, scopedCodes int
	if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FILTER (WHERE type='totp'), count(*) FILTER (WHERE type='recovery_code') FROM system.user_mfa WHERE user_id=$1 AND tenant_id=$2 AND revoked_at IS NULL`, f.userID, f.tenantID).Scan(&scopedFactors, &scopedCodes); err != nil {
		t.Fatal(err)
	}

	if scopedFactors != 1 || scopedCodes != 10 {
		t.Fatalf("scoped factors/codes = %d/%d", scopedFactors, scopedCodes)
	}

	newToken := body["access_token"].(string)
	decision, err := f.checker.EnforceMFA(t.Context(), "/items", f.tenantID, f.authContext(t, newToken))
	if err != nil || decision != enforce.Allowed {
		t.Fatalf("enforcement = %q, %v", decision, err)
	}

	foreign, err := f.handlers.mfa.ListAccepted(t.Context(), f.userID, mfa.Scope{TenantID: other.ID})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range foreign {
		if c.TenantID != nil {
			t.Fatalf("foreign tenant accepts %s", c.ID)
		}
	}

	if n := f.countActive(t, f.userID, mfa.CredentialRecoveryCode); n != 20 {
		t.Fatalf("total recovery codes = %d, want both sets intact", n)
	}
}

func TestConfirm_OnlyForeignTenantFactorCannotProducePlatformFactor(t *testing.T) {
	f := newFixture(t)
	other := mfatest.NewTenant(t, f.conn)
	if err := f.handlers.mfa.WithTx(t.Context(), func(tx *sql.Tx) error {
		_, err := f.handlers.mfa.InsertScopedTx(t.Context(), tx, f.userID, new(other.ID), mfa.CredentialTOTP, []byte("foreign factor"), nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	token := f.login(t, f.userID)
	id, secret := f.begin(t, token)
	status, body := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", token, map[string]any{"enrollment_id": id, "code": currentCode(t, secret)})
	if status != http.StatusOK {
		t.Fatalf("confirm = %d, %v", status, body)
	}

	var platformCount int
	if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM system.user_mfa WHERE user_id=$1 AND tenant_id IS NULL`, f.userID).Scan(&platformCount); err != nil {
		t.Fatal(err)
	}

	if platformCount != 0 {
		t.Fatalf("created %d platform credentials from password-only session", platformCount)
	}
}

func TestConfirm_ResetWithNoRemainingFactorsClearsBarrier(t *testing.T) {
	f := newFixture(t)
	if err := f.handlers.mfa.WithTx(t.Context(), func(tx *sql.Tx) error {
		return f.handlers.mfa.SetResetTx(t.Context(), tx, f.userID, f.tenantID, true)
	}); err != nil {
		t.Fatal(err)
	}

	f.requireMFA(t)
	token := f.login(t, f.userID)
	id, secret := f.begin(t, token)
	status, body := f.post(t, f.handlers.Confirm, "/auth/mfa/enroll/totp/confirm", token, map[string]any{
		"enrollment_id": id,
		"code":          currentCode(t, secret),
	})
	if status != http.StatusOK {
		t.Fatalf("confirm = %d, %v", status, body)
	}

	accepted, err := f.handlers.mfa.ListAccepted(t.Context(), f.userID, mfa.Scope{TenantID: f.tenantID})
	if err != nil {
		t.Fatal(err)
	}

	if len(accepted) != 11 || !mfa.HasFactor(accepted) {
		t.Fatalf("accepted credentials = %v, want factor and ten recovery codes", accepted)
	}

	for _, c := range accepted {
		if c.TenantID == nil || *c.TenantID != f.tenantID {
			t.Fatalf("reset enrolled a credential outside its tenant: %s", c.ID)
		}
	}

	decision, err := f.checker.EnforceMFA(t.Context(), "/items", f.tenantID, f.authContext(t, body["access_token"].(string)))
	if err != nil || decision != enforce.Allowed {
		t.Fatalf("enforcement = %q, %v", decision, err)
	}
}
