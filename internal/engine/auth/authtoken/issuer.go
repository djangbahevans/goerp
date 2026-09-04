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
// token structure". AMR/MFAVerifiedAt reflect LoginParams' MFA fields —
// ["pwd"]/nil for an ordinary password-only login, ["pwd", method]/the
// verification timestamp once a caller (goerp#304's mfa_token branch)
// passes them.
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

	// MFAMethod/MFAVerifiedAt/MFACredentialID are set only when this login
	// already completed MFA verification before Issue is called — e.g.
	// goerp#304's mfa_token branch, issuing the final session after a
	// successful factor check. Left zero-value for an ordinary
	// password-only login.
	MFAMethod       string
	MFAVerifiedAt   *time.Time
	MFACredentialID string
}

// RefreshParams describes the rotating request Refresh is minting a new
// token pair for. DeviceID is the request's own device_id if it
// presented one, else "" — never trusted to select which row rotates,
// only used by session.Store.Rotate to distinguish a same-device
// double-submit from a genuine cross-device replay. UserAgent/IPAddress/
// CountryCode are recorded on the new row the same way LoginParams'
// fields are on a fresh login (auth-internals.md §4 step 7b) — the row
// tracks where the session is currently being used, not frozen at
// whatever the original login saw.
type RefreshParams struct {
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
		ID:              sessionID,
		UserID:          p.UserID,
		TenantID:        t.ID,
		DeviceID:        deviceID,
		RefreshHash:     refreshHash,
		UserAgent:       p.UserAgent,
		IPAddress:       p.IPAddress,
		CountryCode:     p.CountryCode,
		ExpiresAt:       now.Add(refreshTokenTTL),
		MFAMethod:       p.MFAMethod,
		MFAVerifiedAt:   p.MFAVerifiedAt,
		MFACredentialID: p.MFACredentialID,
	}); err != nil {
		return nil, fmt.Errorf("record session: %w", err)
	}

	accessToken, err := i.signAccessToken(sessionID, t.ID, p.UserID, roleNames, p.MFAMethod, p.MFAVerifiedAt, now)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	return &Tokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(accessTokenTTL.Seconds()),
	}, nil
}

// ReissueAccessToken mints a fresh access token for an existing session
// — sessionID/tenantID/userID/roleNames describing that session's
// current state — without creating a new session row or refresh token.
// auth-internals.md §8 "Step-up re-verification" step 2: same session,
// refresh token unchanged, only the access token (and its updated
// amr/mfa_verified_at claims) is refreshed. The caller is responsible for
// persisting the session row's own mfa_verified_at/mfa_method/
// mfa_credential_id columns first (session.Store.UpdateMFAAssurance) —
// this method only signs the token, it doesn't touch the database.
func (i *Issuer) ReissueAccessToken(sessionID, tenantID, userID string, roleNames []string, mfaMethod string, mfaVerifiedAt *time.Time) (accessToken string, expiresIn int, err error) {
	accessToken, err = i.signAccessToken(sessionID, tenantID, userID, roleNames, mfaMethod, mfaVerifiedAt, time.Now())
	if err != nil {
		return "", 0, fmt.Errorf("sign access token: %w", err)
	}
	return accessToken, int(accessTokenTTL.Seconds()), nil
}

// signAccessToken mints and signs the access token. amr always includes
// "pwd"; mfaMethod is appended when non-empty, per auth-internals.md §4's
// documented "an MFA factor type is appended once this session has
// completed MFA" shape — never a replacement for "pwd".
func (i *Issuer) signAccessToken(sessionID, tenantID, userID string, roleNames []string, mfaMethod string, mfaVerifiedAt *time.Time, now time.Time) (string, error) {
	amr := []string{"pwd"}
	var mfaVerifiedAtClaim *int64
	if mfaMethod != "" {
		amr = append(amr, mfaMethod)
	}
	if mfaVerifiedAt != nil {
		unix := mfaVerifiedAt.Unix()
		mfaVerifiedAtClaim = &unix
	}

	claims := Claims{
		Issuer:        issuerName,
		Subject:       userID,
		IssuedAt:      jwt.NewNumericDate(now),
		ExpiresAt:     jwt.NewNumericDate(now.Add(accessTokenTTL)),
		ID:            uuid.NewString(),
		SessionID:     sessionID,
		TenantID:      tenantID,
		Roles:         roleNames,
		Scope:         []string{"api"},
		AMR:           amr,
		MFAVerifiedAt: mfaVerifiedAtClaim,
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
	return token, hashRefreshToken(token), nil
}

// hashRefreshToken hashes an already-generated (or client-presented)
// refresh token the same way newRefreshToken hashes a freshly minted one
// — the lookup key Rotate matches a presented token against.
func hashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Refresh rotates presentedRefreshToken inside session.Store's own
// transactional lookup/rotate/replay-decision sequence (auth-internals.md
// §4 "Refresh token rotation"), then mints a fresh access/refresh token
// pair for the rotated session.
//
// A non-RotateOK outcome is not an error — it's every caller-facing
// rejection (not found, already revoked, a same-device double-submit
// dropped silently, or a cross-device replay that just triggered a
// family-wide revocation as a side effect). The caller maps every one of
// them to a 401; Tokens is nil whenever outcome != session.RotateOK.
//
// Known gap: i.sessions.Rotate commits its own transaction — marking the
// old row rotated and inserting the new one — before the tenant/role
// lookups and access-token signing below run, since which tenant/user to
// resolve is itself only known once Rotate's own SELECT ... FOR UPDATE
// has read the presented token's row. Unlike Issue (which resolves both
// before its own session Insert, because the caller already knows
// TenantSlug/UserID up front), Refresh cannot reorder this without either
// threading session.Store's *sql.Tx across the tenant/role package
// boundary or re-running the lookup/rotate/replay decision as a second,
// separate transaction — the latter reopening the exact TOCTOU race the
// single-transaction design (and TestRotate_ConcurrentRequestsForSameTokenDoNotRace)
// exists to close. A failure in the tenant/role lookup or signing after a
// successful rotation returns a bare error with the rotation already
// committed — the presented refresh token is dead and the freshly minted
// replacement was never returned to the caller, so that session is
// unreachable until the user logs in again. A narrow, rare-in-practice
// gap: the tenant/user a just-committed row references failing to
// resolve moments later implies a concurrent tenant deletion or a
// DB-level fault that would likely have already surfaced inside Rotate
// itself.
func (i *Issuer) Refresh(ctx context.Context, presentedRefreshToken string, p RefreshParams) (*Tokens, session.RotateOutcome, error) {
	presentedHash := hashRefreshToken(presentedRefreshToken)

	newToken, newHash, err := newRefreshToken()
	if err != nil {
		return nil, 0, fmt.Errorf("generate refresh token: %w", err)
	}
	newSessionID := uuid.NewString()

	result, err := i.sessions.Rotate(ctx, presentedHash, newSessionID, newHash, p.DeviceID, time.Now().Add(refreshTokenTTL), p.UserAgent, p.IPAddress, p.CountryCode)
	if err != nil {
		return nil, 0, fmt.Errorf("rotate session: %w", err)
	}
	if result.Outcome != session.RotateOK {
		return nil, result.Outcome, nil
	}

	t, err := i.tenants.GetByID(ctx, result.TenantID)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve tenant %s: %w", result.TenantID, err)
	}
	roleNames, err := i.roles.RoleNamesForUser(ctx, t.Slug, result.UserID)
	if err != nil {
		return nil, 0, fmt.Errorf("look up roles for user %s: %w", result.UserID, err)
	}

	accessToken, err := i.signAccessToken(newSessionID, result.TenantID, result.UserID, roleNames, result.MFAMethod, result.MFAVerifiedAt, time.Now())
	if err != nil {
		return nil, 0, fmt.Errorf("sign access token: %w", err)
	}

	return &Tokens{
		AccessToken:  accessToken,
		RefreshToken: newToken,
		ExpiresIn:    int(accessTokenTTL.Seconds()),
	}, session.RotateOK, nil
}
