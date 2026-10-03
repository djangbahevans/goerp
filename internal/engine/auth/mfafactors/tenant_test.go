package mfafactors

import (
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/mfa/mfatest"
)

func (f *fixture) setTenantFactor(t *testing.T, id, tenantID string) {
	t.Helper()
	if _, err := f.conn.ExecContext(t.Context(), `UPDATE system.user_mfa SET tenant_id=$2 WHERE id=$1`, id, tenantID); err != nil {
		t.Fatal(err)
	}
}

func TestRemove_PlatformFactorRejectsTenantProof(t *testing.T) {
	f := newFixture(t)
	platform, _ := f.seedTOTP(t, "Platform")
	own, code := f.seedTOTP(t, "Tenant")
	f.setTenantFactor(t, own.ID, f.tenantID)
	accessToken := f.login(t)
	status, body := f.remove(t, accessToken, platform.ID, map[string]string{"type": "totp", "code": code})
	if status != http.StatusUnauthorized || errorCode(body) != "invalid_mfa_code" {
		t.Fatalf("platform removal with tenant proof = %d, %v", status, body)
	}

	status, body = f.remove(t, accessToken, own.ID, map[string]string{"type": "totp", "code": code})
	if status != http.StatusNoContent {
		t.Fatalf("tenant removal with tenant proof = %d, %v", status, body)
	}

	if f.countActive(t, "totp") != 1 {
		t.Fatal("platform factor was not preserved")
	}
}

func TestListAndRemoval_IsolateTenantFactorsAndSessions(t *testing.T) {
	f := newFixture(t)
	other := mfatest.NewTenant(t, f.conn)
	mfatest.AddMember(t, f.conn, other, f.userID)
	platform, _ := f.seedTOTP(t, "Platform")
	own, code := f.seedTOTP(t, "Tenant")
	foreign, _ := f.seedTOTP(t, "Foreign")
	f.setTenantFactor(t, own.ID, f.tenantID)
	f.setTenantFactor(t, foreign.ID, other.ID)
	accessToken := f.login(t)
	otherSession := uuid.NewV7().String()
	if err := session.NewStore(f.conn).Insert(t.Context(), session.Row{
		ID: otherSession, UserID: f.userID, TenantID: other.ID,
		DeviceID: uuid.NewV7().String(), RefreshHash: otherSession, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	body := f.list(t, accessToken)
	factors := body["factors"].([]any)
	if len(factors) != 2 {
		t.Fatalf("listed factors = %v", factors)
	}

	for _, raw := range factors {
		factor := raw.(map[string]any)
		switch factor["id"] {
		case own.ID:
			if factor["tenant_only"] != true {
				t.Errorf("tenant factor scope: %v", factor)
			}
		case platform.ID:
			if factor["tenant_only"] != false {
				t.Errorf("platform factor scope: %v", factor)
			}
		default:
			t.Errorf("foreign factor listed: %v", factor)
		}
	}

	status, body := f.remove(t, accessToken, foreign.ID, map[string]string{"type": "totp", "code": "000000"})
	if status != http.StatusNotFound {
		t.Fatalf("foreign removal = %d, %v", status, body)
	}

	status, body = f.remove(t, accessToken, own.ID, map[string]string{"type": "totp", "code": code})
	if status != http.StatusNoContent {
		t.Fatalf("tenant removal = %d, %v", status, body)
	}

	var revoked bool
	if err := f.conn.QueryRowContext(t.Context(), `SELECT revoked_at IS NOT NULL FROM system.sessions WHERE id=$1`, otherSession).Scan(&revoked); err != nil {
		t.Fatal(err)
	}

	if revoked || f.sessionValid(t, accessToken) {
		t.Fatalf("other session revoked=%v, own session valid=%v", revoked, f.sessionValid(t, accessToken))
	}
}

func TestRegenerate_TenantProofPreservesPlatformCodes(t *testing.T) {
	f := newFixture(t)
	f.seedTOTP(t, "Platform")
	own, code := f.seedTOTP(t, "Tenant")
	f.setTenantFactor(t, own.ID, f.tenantID)
	f.seedRecoveryCode(t, "AAAAA-BBBBB")
	f.seedRecoveryCode(t, "CCCCC-DDDDD")
	if _, err := f.conn.ExecContext(t.Context(), `UPDATE system.user_mfa SET tenant_id=$2
 WHERE id=(SELECT id FROM system.user_mfa WHERE user_id=$1 AND type='recovery_code' ORDER BY created_at DESC LIMIT 1)`, f.userID, f.tenantID); err != nil {
		t.Fatal(err)
	}

	accessToken := f.login(t)
	status, body := f.regenerate(t, accessToken, map[string]string{"type": "totp", "code": code})
	if status != http.StatusOK {
		t.Fatalf("regenerate = %d, %v", status, body)
	}

	var platformCodes, tenantCodes int
	if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FILTER (WHERE tenant_id IS NULL),
 count(*) FILTER (WHERE tenant_id=$2) FROM system.user_mfa
 WHERE user_id=$1 AND type='recovery_code' AND revoked_at IS NULL`, f.userID, f.tenantID).Scan(&platformCodes, &tenantCodes); err != nil {
		t.Fatal(err)
	}

	if platformCodes != 1 || tenantCodes != 10 || !f.sessionValid(t, accessToken) {
		t.Fatalf("platform codes=%d, tenant codes=%d, current session valid=%v", platformCodes, tenantCodes, f.sessionValid(t, accessToken))
	}
}
