package mfatoken

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testKey() *Key {
	return &Key{KeyID: "test-kid", Secret: []byte("0123456789abcdef0123456789abcdef")}
}

func TestIssueThenVerify_RoundTrips(t *testing.T) {
	codec := NewCodec(testKey())

	token, txn, err := codec.Issue("user-1", "tenant-1", "https://acmecorp.goerp.io")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if token == "" || txn == "" {
		t.Fatal("Issue() returned empty token or txn")
	}

	claims, err := codec.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "user-1")
	}
	if claims.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want %q", claims.TenantID, "tenant-1")
	}
	if claims.Txn != txn {
		t.Errorf("Txn = %q, want %q", claims.Txn, txn)
	}
	if claims.Purpose != PurposeMFALogin {
		t.Errorf("Purpose = %q, want %q", claims.Purpose, PurposeMFALogin)
	}
	if claims.Origin != "https://acmecorp.goerp.io" {
		t.Errorf("Origin = %q, want %q", claims.Origin, "https://acmecorp.goerp.io")
	}
}

func TestIssue_DistinctTxnPerCall(t *testing.T) {
	codec := NewCodec(testKey())

	_, txn1, err := codec.Issue("user-1", "tenant-1", "https://acmecorp.goerp.io")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	_, txn2, err := codec.Issue("user-1", "tenant-1", "https://acmecorp.goerp.io")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	if txn1 == txn2 {
		t.Error("two Issue() calls returned the same txn")
	}
}

func TestVerify_RejectsExpiredToken(t *testing.T) {
	key := testKey()
	codec := NewCodec(key)

	now := time.Now().Add(-10 * time.Minute)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(TTL)), // expired 5 minutes ago
		},
		TenantID: "tenant-1",
		Txn:      "txn-1",
		Purpose:  PurposeMFALogin,
		Origin:   "https://acmecorp.goerp.io",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = key.KeyID
	signed, err := tok.SignedString(key.Secret)
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}

	if _, err := codec.Verify(signed); err == nil {
		t.Error("Verify() succeeded on an expired token, want error")
	}
}

func TestVerify_RejectsWrongKid(t *testing.T) {
	issuingKey := testKey()
	verifyingKey := &Key{KeyID: "different-kid", Secret: issuingKey.Secret}

	issuer := NewCodec(issuingKey)
	verifier := NewCodec(verifyingKey)

	token, _, err := issuer.Issue("user-1", "tenant-1", "https://acmecorp.goerp.io")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	if _, err := verifier.Verify(token); err == nil {
		t.Error("Verify() succeeded with a mismatched kid, want error")
	}
}

func TestVerify_RejectsWrongSecret(t *testing.T) {
	issuer := NewCodec(testKey())
	verifier := NewCodec(&Key{KeyID: "test-kid", Secret: []byte("different-secret-different-secr")})

	token, _, err := issuer.Issue("user-1", "tenant-1", "https://acmecorp.goerp.io")
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	if _, err := verifier.Verify(token); err == nil {
		t.Error("Verify() succeeded with a mismatched secret, want error")
	}
}

func TestVerify_RejectsWrongPurpose(t *testing.T) {
	key := testKey()
	codec := NewCodec(key)

	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(TTL)),
		},
		TenantID: "tenant-1",
		Txn:      "txn-1",
		Purpose:  "something_else",
		Origin:   "https://acmecorp.goerp.io",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = key.KeyID
	signed, err := tok.SignedString(key.Secret)
	if err != nil {
		t.Fatalf("SignedString() error: %v", err)
	}

	if _, err := codec.Verify(signed); err == nil {
		t.Error("Verify() succeeded on a token with the wrong purpose, want error")
	}
}

func TestVerify_RejectsMalformedToken(t *testing.T) {
	codec := NewCodec(testKey())

	if _, err := codec.Verify("not-a-real-token"); err == nil {
		t.Error("Verify() succeeded on a malformed token, want error")
	}
}
