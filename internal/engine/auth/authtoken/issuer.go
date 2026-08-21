// Package authtoken issues the dual-token session model — a short-lived
// RS256 JWT access token and a long-lived opaque refresh token — for an
// already-authenticated login (auth-internals.md §4 "Token architecture").
// Password/credential verification, refresh rotation, and revocation are
// separate tickets (goerp#147); this package only mints the first pair and
// records the new sessions row.
package authtoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 30 * 24 * time.Hour
	issuerName      = "goerp"
)

// Claims is the access token's JSON shape, auth-internals.md §4 "Access
// token structure". Every token this package issues has AMR=["pwd"] and
// MFAVerifiedAt=nil — MFA support is a separate ticket (§8).
type Claims struct {
	jwt.RegisteredClaims
	SessionID     string   `json:"sid"`
	TenantID      string   `json:"tid"`
	Roles         []string `json:"roles"`
	Scope         []string `json:"scp"`
	AMR           []string `json:"amr"`
	MFAVerifiedAt *int64   `json:"mfa_verified_at"`
}

// Issuer mints access/refresh token pairs for an already-authenticated
// login.
type Issuer struct {
	signingKey *signingkey.SigningKey
	tenants    *tenant.Store
	roles      *role.Store
	sessions   *session.Store
}

func NewIssuer(signingKey *signingkey.SigningKey, tenants *tenant.Store, roles *role.Store, sessions *session.Store) *Issuer {
	return &Issuer{signingKey: signingKey, tenants: tenants, roles: roles, sessions: sessions}
}

// LoginParams describes the login event Issue is minting tokens for.
// DeviceID is generated when empty (a first-ever login with no existing
// device cookie); UserAgent/IPAddress/CountryCode are recorded on the new
// session row as-is and stay unset when empty.
type LoginParams struct {
	UserID      string
	TenantSlug  string
	DeviceID    string
	UserAgent   string
	IPAddress   string
	CountryCode string
}

// Tokens is one issued access/refresh token pair.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // access token lifetime in seconds
}

// Issue mints a new access/refresh token pair and records the refresh
// token's hash as a new sessions row (id == family_id: this is always a
// fresh family's first row, never a rotation).
func (i *Issuer) Issue(ctx context.Context, p LoginParams) (*Tokens, error) {
	t, err := i.tenants.GetBySlug(ctx, p.TenantSlug)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant %q: %w", p.TenantSlug, err)
	}

	roleNames, err := i.roles.RoleNamesForUser(ctx, p.TenantSlug, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("look up roles for user %s: %w", p.UserID, err)
	}

	deviceID := p.DeviceID
	if deviceID == "" {
		deviceID = uuid.NewString()
	}

	refreshToken, refreshHash, err := newRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	now := time.Now()
	sessionID := uuid.NewString()

	if err := i.sessions.Insert(ctx, session.Row{
		ID:          sessionID,
		UserID:      p.UserID,
		TenantID:    t.ID,
		DeviceID:    deviceID,
		RefreshHash: refreshHash,
		UserAgent:   p.UserAgent,
		IPAddress:   p.IPAddress,
		CountryCode: p.CountryCode,
		ExpiresAt:   now.Add(refreshTokenTTL),
	}); err != nil {
		return nil, fmt.Errorf("record session: %w", err)
	}

	accessToken, err := i.signAccessToken(sessionID, t.ID, p.UserID, roleNames, now)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	return &Tokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(accessTokenTTL.Seconds()),
	}, nil
}

func (i *Issuer) signAccessToken(sessionID, tenantID, userID string, roleNames []string, now time.Time) (string, error) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuerName,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenTTL)),
			ID:        uuid.NewString(),
		},
		SessionID: sessionID,
		TenantID:  tenantID,
		Roles:     roleNames,
		Scope:     []string{"api"},
		AMR:       []string{"pwd"},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = i.signingKey.KID
	return token.SignedString(i.signingKey.Private)
}

// newRefreshToken returns a fresh opaque token (32 CSPRNG bytes,
// base64url-encoded) and the hex-encoded SHA-256 hash to persist —
// auth-internals.md §4's "Refresh token" and Session table column doc.
func newRefreshToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}
