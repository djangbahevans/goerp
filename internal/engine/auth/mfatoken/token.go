package mfatoken

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TTL is the mfa_token's lifetime — auth-internals.md §8 "MFA token
// flow"'s 5-minute window, the only time a caller has to complete MFA
// verification for one specific login attempt.
const TTL = 5 * time.Minute

// PurposeMFALogin is the only Claims.Purpose value this package issues or
// accepts today — carried explicitly (rather than left implicit) so a
// future short-lived-token shape that happens to reuse this same claim
// set can't be replayed here by accident.
const PurposeMFALogin = "mfa_login"

// ErrInvalidToken covers every way an mfa_token can fail to verify:
// malformed, wrong signature, expired, or the wrong Purpose. Callers
// don't need to distinguish these — auth-internals.md §8 treats all of
// them as "reject with 401", never a distinct response.
var ErrInvalidToken = errors.New("invalid or expired mfa_token")

// Claims is the mfa_token's JSON shape, auth-internals.md §8's exact
// {sub, tid, txn, purpose, origin, iat, exp} claim set — sub/iat/exp come
// from jwt.RegisteredClaims.
type Claims struct {
	jwt.RegisteredClaims
	TenantID string `json:"tid"`
	Txn      string `json:"txn"`
	Purpose  string `json:"purpose"`
	Origin   string `json:"origin"`
}

// Codec issues and verifies mfa_tokens against a single HMAC-SHA256 key.
// The same Codec instance is shared between loginflow (Issue) and the
// /auth/mfa/verify handler (Verify), since both must agree on the same
// key — see engine.go's wiring.
type Codec struct {
	key *Key
}

func NewCodec(key *Key) *Codec {
	return &Codec{key: key}
}

// Issue mints a new mfa_token for userID completing login into tenantID,
// bound to origin (the Origin header captured at password-login time).
// The returned txn is the same random per-attempt identifier embedded in
// the token's txn claim — the atomic single-use consumption key the
// /auth/mfa/verify handler claims via Redis SETNX.
func (c *Codec) Issue(userID, tenantID, origin string) (token, txn string, err error) {
	now := time.Now()
	txn = uuid.NewString()

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(TTL)),
		},
		TenantID: tenantID,
		Txn:      txn,
		Purpose:  PurposeMFALogin,
		Origin:   origin,
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = c.key.KeyID
	signed, err := tok.SignedString(c.key.Secret)
	if err != nil {
		return "", "", fmt.Errorf("sign mfa_token: %w", err)
	}
	return signed, txn, nil
}

// Verify validates rawToken's HMAC signature, expiry, and Purpose, and
// returns its claims. It does not check the txn consumption marker, the
// Origin header, or the MFA attempt lockout counter — those are the
// caller's job (auth-internals.md §8 steps 3-5), since they depend on the
// current request and Redis state, not the token alone.
func (c *Codec) Verify(rawToken string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(rawToken, claims, c.keyFunc, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if claims.Purpose != PurposeMFALogin {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// keyFunc returns the secret to verify token against. Only the Active key
// exists today (no rotation/Previous list yet, same as
// authcheck.Checker.keyFunc) — a kid that doesn't match it is always
// unrecognized.
func (c *Codec) keyFunc(token *jwt.Token) (any, error) {
	kid, ok := token.Header["kid"].(string)
	if !ok || kid != c.key.KeyID {
		return nil, fmt.Errorf("unrecognized signing key kid %v", token.Header["kid"])
	}
	return c.key.Secret, nil
}
