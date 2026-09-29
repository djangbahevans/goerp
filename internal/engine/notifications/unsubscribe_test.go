package notifications

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/golang-jwt/jwt/v5"
)

func newTestSigningKey(t *testing.T, kid string) signingkey.SigningKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return signingkey.SigningKey{KID: kid, Algorithm: "RS256", Private: priv, Public: &priv.PublicKey}
}

func TestUnsubscribeCodec_RoundTrips(t *testing.T) {
	c := NewUnsubscribeCodec(&signingkey.SigningKeySet{Active: newTestSigningKey(t, "k1")})
	tok, err := c.Issue("user-1", "tenant-1", "sales.order_confirmed")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	claims, err := c.Verify(tok)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if claims.Subject != "user-1" || claims.TenantID != "tenant-1" || claims.NotificationType != "sales.order_confirmed" {
		t.Errorf("claims = %+v", claims)
	}
	if exp := claims.ExpiresAt.Time; exp.Before(time.Now().Add(UnsubscribeTokenTTL - time.Minute)) {
		t.Errorf("expires at %v, want about %v from now", exp, UnsubscribeTokenTTL)
	}
}

func TestUnsubscribeCodec_AcceptsAPreviousKey(t *testing.T) {
	old := newTestSigningKey(t, "old")
	tok, err := NewUnsubscribeCodec(&signingkey.SigningKeySet{Active: old}).Issue("user-1", "tenant-1", "t")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	rotated := NewUnsubscribeCodec(&signingkey.SigningKeySet{Active: newTestSigningKey(t, "new"), Previous: []signingkey.SigningKey{old}})
	if _, err := rotated.Verify(tok); err != nil {
		t.Errorf("Verify() with the signing key in Previous: %v", err)
	}
}

func TestUnsubscribeCodec_RejectsBadTokens(t *testing.T) {
	key := newTestSigningKey(t, "k1")
	c := NewUnsubscribeCodec(&signingkey.SigningKeySet{Active: key})
	good, err := c.Issue("user-1", "tenant-1", "sales.order_confirmed")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	sign := func(k signingkey.SigningKey, claims jwt.Claims) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = k.KID
		s, err := tok.SignedString(k.Private)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return s
	}
	valid := func() UnsubscribeClaims {
		return UnsubscribeClaims{
			Issuer:    unsubscribeIssuer,
			Subject:   "user-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			TenantID:  "tenant-1", NotificationType: "t", Purpose: unsubscribePurpose,
		}
	}
	parts := strings.Split(good, ".")

	cases := map[string]string{
		"empty":                   "",
		"garbage":                 "not-a-jwt",
		"tampered claims":         parts[0] + "." + strings.TrimRight(parts[1], "A") + "B." + parts[2],
		"another key's signature": sign(signingkey.SigningKey{KID: "k1", Private: newTestSigningKey(t, "x").Private}, valid()),
		"unknown kid":             sign(newTestSigningKey(t, "k2"), valid()),
		"expired": func() string {
			c := valid()
			c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
			return sign(key, c)
		}(),
		"no expiry": func() string {
			c := valid()
			c.ExpiresAt = nil
			return sign(key, c)
		}(),
		"wrong purpose": func() string {
			c := valid()
			c.Purpose = "mfa_login"
			return sign(key, c)
		}(),
		"no notification type": func() string {
			c := valid()
			c.NotificationType = ""
			return sign(key, c)
		}(),
		"an access token": sign(key, authtoken.Claims{
			Issuer: "goerp", Subject: "user-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			SessionID: "s", TenantID: "tenant-1",
		}),
	}
	for name, tok := range cases {
		if _, err := c.Verify(tok); !errors.Is(err, ErrInvalidUnsubscribeToken) {
			t.Errorf("%s: Verify() error = %v, want ErrInvalidUnsubscribeToken", name, err)
		}
	}
}

// TestUnsubscribeCodec_TokenIsNotAnAccessToken parses an unsubscribe
// token the way authcheck.Checker parses a bearer token: signed with the
// same key, it must still fail there.
func TestUnsubscribeCodec_TokenIsNotAnAccessToken(t *testing.T) {
	key := newTestSigningKey(t, "k1")
	tok, err := NewUnsubscribeCodec(&signingkey.SigningKeySet{Active: key}).Issue("user-1", "tenant-1", "t")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	_, err = jwt.ParseWithClaims(tok, &authtoken.Claims{}, func(*jwt.Token) (any, error) { return key.Public, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer("goerp"))
	if err == nil {
		t.Fatal("an unsubscribe token parsed as an access token")
	}
}

func TestUnsubscribeCodec_IssueAtIsDeterministic(t *testing.T) {
	c := NewUnsubscribeCodec(&signingkey.SigningKeySet{Active: newTestSigningKey(t, "k1")})
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	a, err := c.IssueAt("user-1", "tenant-1", "sales.order_confirmed", at)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.IssueAt("user-1", "tenant-1", "sales.order_confirmed", at)
	if a != b {
		t.Error("IssueAt minted two different tokens for the same arguments")
	}
	if _, err := c.Verify(a); err != nil {
		t.Errorf("Verify(IssueAt token) error: %v", err)
	}
}
