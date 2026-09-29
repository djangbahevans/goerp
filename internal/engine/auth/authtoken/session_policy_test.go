package authtoken

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionpolicy"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

const testDeviceID = "11111111-1111-1111-1111-111111111111"

// withSessionPolicy saves policy for f's tenant, wires the real store into
// f.issuer, and puts the issuer on a clock the test advances. The
// returned save changes the policy later.
func withSessionPolicy(t *testing.T, f *fixture, policy sessionpolicy.Policy) (now *time.Time, save func(sessionpolicy.Policy)) {
	t.Helper()
	ctx := t.Context()
	config := tenantconfig.NewStore(f.conn)
	if err := config.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}
	var tenantID string
	if err := f.conn.QueryRow(`SELECT id FROM system.tenants WHERE slug = $1`, f.tenantSlug).Scan(&tenantID); err != nil {
		t.Fatalf("read fixture tenant id: %v", err)
	}
	store := sessionpolicy.NewStore(config)
	save = func(policy sessionpolicy.Policy) {
		t.Helper()
		if err := store.Save(ctx, tenantID, policy); err != nil {
			t.Fatalf("Save() error: %v", err)
		}
	}
	save(policy)
	f.issuer.SetSessionPolicies(store)

	clock := time.Now()
	f.issuer.now = func() time.Time { return clock }
	return &clock, save
}

func TestSessionPolicy_IdleTimeoutEndsAnUnrefreshedSession(t *testing.T) {
	f := newFixture(t)
	now, _ := withSessionPolicy(t, f, sessionpolicy.Policy{IdleTimeout: 30 * time.Minute})

	tokens, err := f.issuer.Issue(t.Context(), LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug, DeviceID: testDeviceID, Persistent: true})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	// Within the idle window: the session lives on, and the window
	// restarts from this refresh.
	*now = now.Add(29 * time.Minute)
	tokens, outcome, err := f.issuer.Refresh(t.Context(), tokens.RefreshToken, RefreshParams{DeviceID: testDeviceID})
	if err != nil || outcome != session.RotateOK {
		t.Fatalf("Refresh() inside the idle window = %v, %v, want RotateOK", outcome, err)
	}

	*now = now.Add(29 * time.Minute)
	tokens, outcome, err = f.issuer.Refresh(t.Context(), tokens.RefreshToken, RefreshParams{DeviceID: testDeviceID})
	if err != nil || outcome != session.RotateOK {
		t.Fatalf("Refresh() inside the restarted idle window = %v, %v, want RotateOK", outcome, err)
	}

	*now = now.Add(31 * time.Minute)
	if _, outcome, err := f.issuer.Refresh(t.Context(), tokens.RefreshToken, RefreshParams{DeviceID: testDeviceID}); err != nil || outcome != session.RotateExpired {
		t.Fatalf("Refresh() after the idle window = %v, %v, want RotateExpired", outcome, err)
	}
}

func TestSessionPolicy_LoweredIdleTimeoutAppliesToNextSession(t *testing.T) {
	f := newFixture(t)
	now, save := withSessionPolicy(t, f, sessionpolicy.Policy{IdleTimeout: 8 * time.Hour})

	before, err := f.issuer.Issue(t.Context(), LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug, DeviceID: testDeviceID, Persistent: true})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	save(sessionpolicy.Policy{IdleTimeout: 15 * time.Minute})
	after, err := f.issuer.Issue(t.Context(), LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug, DeviceID: testDeviceID, Persistent: true})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	*now = now.Add(16 * time.Minute)
	if _, outcome, err := f.issuer.Refresh(t.Context(), after.RefreshToken, RefreshParams{DeviceID: testDeviceID}); err != nil || outcome != session.RotateExpired {
		t.Fatalf("Refresh() of the session issued after lowering = %v, %v, want RotateExpired", outcome, err)
	}
	// A session issued earlier keeps the expiry it was written with until
	// its next refresh, which picks up the lowered window.
	if _, outcome, err := f.issuer.Refresh(t.Context(), before.RefreshToken, RefreshParams{DeviceID: testDeviceID}); err != nil || outcome != session.RotateOK {
		t.Fatalf("Refresh() of the session issued before lowering = %v, %v, want RotateOK", outcome, err)
	}
}

func TestSessionPolicy_AbsoluteMaxEndsAnActiveSession(t *testing.T) {
	f := newFixture(t)
	now, _ := withSessionPolicy(t, f, sessionpolicy.Policy{AbsoluteMax: 2 * time.Hour})

	tokens, err := f.issuer.Issue(t.Context(), LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug, DeviceID: testDeviceID, Persistent: true})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	for range 7 {
		*now = now.Add(15 * time.Minute)
		var outcome session.RotateOutcome
		tokens, outcome, err = f.issuer.Refresh(t.Context(), tokens.RefreshToken, RefreshParams{DeviceID: testDeviceID})
		if err != nil || outcome != session.RotateOK {
			t.Fatalf("Refresh() at %s = %v, %v, want RotateOK", now.Format(time.TimeOnly), outcome, err)
		}
	}
	// 1h45m in, the access token is cut to the at most 15 minutes the
	// session has left.
	if tokens.ExpiresIn > int((15 * time.Minute).Seconds()) {
		t.Errorf("ExpiresIn = %d, want at most the session's remaining 900s", tokens.ExpiresIn)
	}

	*now = now.Add(20 * time.Minute)
	if _, outcome, err := f.issuer.Refresh(t.Context(), tokens.RefreshToken, RefreshParams{DeviceID: testDeviceID}); err != nil || outcome != session.RotateExpired {
		t.Fatalf("Refresh() past the absolute maximum = %v, %v, want RotateExpired", outcome, err)
	}
}

func TestSessionPolicy_UnsetKeepsTheRefreshTTL(t *testing.T) {
	f := newFixture(t)
	now, _ := withSessionPolicy(t, f, sessionpolicy.Policy{})

	tokens, err := f.issuer.Issue(t.Context(), LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug, DeviceID: testDeviceID})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	*now = now.Add(NonPersistentRefreshTTL - time.Minute)
	if _, outcome, err := f.issuer.Refresh(t.Context(), tokens.RefreshToken, RefreshParams{DeviceID: testDeviceID}); err != nil || outcome != session.RotateOK {
		t.Fatalf("Refresh() inside the refresh TTL = %v, %v, want RotateOK", outcome, err)
	}
}

func TestReissueAccessToken_CappedAtTheSessionEnd(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	f.issuer.now = func() time.Time { return now }

	_, expiresIn, err := f.issuer.ReissueAccessToken("session", "tenant", f.userID, nil, "totp", &now, false, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("ReissueAccessToken() error: %v", err)
	}
	if want := int((5 * time.Minute).Seconds()); expiresIn != want {
		t.Errorf("ExpiresIn = %d, want the session's remaining %d", expiresIn, want)
	}
}

type allowlistOf struct{ allowed string }

func (a allowlistOf) Check(_ context.Context, _, ip string) (bool, error) {
	return ip == a.allowed, nil
}

func TestIssue_IPAllowlist(t *testing.T) {
	f := newFixture(t)
	f.issuer.SetIPAllowlists(allowlistOf{allowed: "203.0.113.7"})

	if _, err := f.issuer.Issue(t.Context(), LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug, IPAddress: "198.51.100.1"}); !errors.Is(err, ErrIPNotAllowed) {
		t.Errorf("Issue() from outside the allowlist error = %v, want ErrIPNotAllowed", err)
	}
	var n int
	if err := f.conn.QueryRow(`SELECT COUNT(*) FROM system.sessions WHERE user_id = $1`, f.userID).Scan(&n); err != nil || n != 0 {
		t.Errorf("sessions after a refused Issue = %d, %v, want 0", n, err)
	}
	if _, err := f.issuer.Issue(t.Context(), LoginParams{UserID: f.userID, TenantSlug: f.tenantSlug, IPAddress: "203.0.113.7"}); err != nil {
		t.Errorf("Issue() from inside the allowlist error = %v", err)
	}
}
