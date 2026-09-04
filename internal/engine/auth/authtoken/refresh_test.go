package authtoken

import (
	"context"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
)

func TestRefresh_LiveTokenReturnsNewTokenPair(t *testing.T) {
	f := newFixture(t)
	first, err := f.issuer.Issue(context.Background(), LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	tokens, outcome, err := f.issuer.Refresh(context.Background(), first.RefreshToken, RefreshParams{DeviceID: "11111111-1111-1111-1111-111111111111"})
	if err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	if outcome != session.RotateOK {
		t.Fatalf("outcome = %v, want RotateOK", outcome)
	}
	if tokens.AccessToken == first.AccessToken {
		t.Error("AccessToken unchanged, want a freshly signed token")
	}
	if tokens.RefreshToken == first.RefreshToken {
		t.Error("RefreshToken == presented token, want a freshly minted one")
	}
	if tokens.ExpiresIn <= 0 {
		t.Errorf("ExpiresIn = %d, want positive", tokens.ExpiresIn)
	}

	// The new refresh token must itself be usable — proves Refresh wired
	// the new hash into the row it actually inserted, not just returned
	// a plausible-looking string.
	second, outcome, err := f.issuer.Refresh(context.Background(), tokens.RefreshToken, RefreshParams{DeviceID: "11111111-1111-1111-1111-111111111111"})
	if err != nil {
		t.Fatalf("second Refresh() error: %v", err)
	}
	if outcome != session.RotateOK {
		t.Fatalf("second outcome = %v, want RotateOK", outcome)
	}
	if second.AccessToken == tokens.AccessToken {
		t.Error("second AccessToken unchanged, want a freshly signed token")
	}
}

func TestRefresh_UnknownTokenRejected(t *testing.T) {
	f := newFixture(t)

	tokens, outcome, err := f.issuer.Refresh(context.Background(), "not-a-real-refresh-token", RefreshParams{})
	if err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	if outcome != session.RotateNotFound {
		t.Errorf("outcome = %v, want RotateNotFound", outcome)
	}
	if tokens != nil {
		t.Errorf("tokens = %+v, want nil", tokens)
	}
}

func TestRefresh_AlreadyRotatedTokenFromDifferentDeviceRevokesFamily(t *testing.T) {
	f := newFixture(t)
	first, err := f.issuer.Issue(context.Background(), LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	if _, outcome, err := f.issuer.Refresh(context.Background(), first.RefreshToken, RefreshParams{DeviceID: "11111111-1111-1111-1111-111111111111"}); err != nil {
		t.Fatalf("first Refresh() error: %v", err)
	} else if outcome != session.RotateOK {
		t.Fatalf("first outcome = %v, want RotateOK", outcome)
	}

	_, outcome, err := f.issuer.Refresh(context.Background(), first.RefreshToken, RefreshParams{DeviceID: "22222222-2222-2222-2222-222222222222"})
	if err != nil {
		t.Fatalf("replay Refresh() error: %v", err)
	}
	if outcome != session.RotateReplayDifferentDevice {
		t.Errorf("outcome = %v, want RotateReplayDifferentDevice", outcome)
	}
}
