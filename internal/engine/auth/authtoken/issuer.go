// Package authtoken issues an RS256 access token and an opaque refresh token for an
// authenticated login and records the new session.
package authtoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionpolicy"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
)

const (
	accessTokenTTL = 15 * time.Minute
	// PersistentRefreshTTL applies to a "remember this device" login (and
	// every non-browser login); NonPersistentRefreshTTL to a browser login
	// without it. Both roll forward on each rotation.
	PersistentRefreshTTL    = 30 * 24 * time.Hour
	NonPersistentRefreshTTL = 12 * time.Hour
	issuerName              = "goerp"
)

func refreshTTL(persistent bool) time.Duration {
	if persistent {
		return PersistentRefreshTTL
	}

	return NonPersistentRefreshTTL
}

// Claims describes an access token. AMR and MFAVerifiedAt carry the login's MFA assurance;
// password-only logins use ["pwd"] and a nil timestamp.
type Claims struct {
	jwt.RegisteredClaims
	SessionID     string   `json:"sid"`
	TenantID      string   `json:"tid"`
	Roles         []string `json:"roles"`
	Scope         []string `json:"scp"`
	AMR           []string `json:"amr"`
	MFAVerifiedAt *int64   `json:"mfa_verified_at"`
	// PasswordChangeRequired mirrors sessions.password_change_required
	// (auth-internals.md §3 "Password policy at sign-in").
	PasswordChangeRequired bool `json:"pcr,omitzero"`
}

// ErrIPNotAllowed rejects an Issue whose IPAddress the tenant's login IP
// allowlist doesn't admit.
var ErrIPNotAllowed = errors.New("signing in to this tenant is not allowed from this address")

// IPAllowlists is satisfied by ipallowlist.Store.
type IPAllowlists interface {
	Check(ctx context.Context, tenantID, ip string) (bool, error)
}

// SessionPolicies is satisfied by sessionpolicy.Store.
type SessionPolicies interface {
	Load(ctx context.Context, tenantID string) (sessionpolicy.Policy, error)
}

// Issuer mints access/refresh token pairs for an already-authenticated
// login.
type Issuer struct {
	signingKey *signingkey.SigningKey
	tenants    *tenant.Store
	roles      *role.Store
	sessions   *session.Store
	policies   SessionPolicies
	allowlists IPAllowlists
	now        func() time.Time
}

func NewIssuer(signingKey *signingkey.SigningKey, tenants *tenant.Store, roles *role.Store, sessions *session.Store) *Issuer {
	return &Issuer{signingKey: signingKey, tenants: tenants, roles: roles, sessions: sessions, now: time.Now}
}

// SetSessionPolicies bounds every session this Issuer writes by its
// tenant's idle timeout and absolute maximum. Without it, a session
// lives out its refresh token TTL.
func (i *Issuer) SetSessionPolicies(policies SessionPolicies) {
	i.policies = policies
}

// SetIPAllowlists makes Issue refuse, with ErrIPNotAllowed, a login from
// outside its tenant's IP allowlist. Every flow that signs a user in
// issues through here, so this is where the allowlist holds for all of
// them; refreshing an existing session isn't a sign-in and isn't checked.
func (i *Issuer) SetIPAllowlists(allowlists IPAllowlists) {
	i.allowlists = allowlists
}

// expiresAt is a session row's expires_at when written at now, for a
// family in tenantID that logged in at familyStart. A policy that can't
// be read fails the write rather than issuing an unbounded session.
func (i *Issuer) expiresAt(ctx context.Context, tenantID string, persistent bool, now, familyStart time.Time) (time.Time, error) {
	var policy sessionpolicy.Policy
	if i.policies != nil {
		var err error
		if policy, err = i.policies.Load(ctx, tenantID); err != nil {
			return time.Time{}, err
		}
	}

	return policy.ExpiresAt(now, familyStart, refreshTTL(persistent)), nil
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
	Persistent  bool

	// MFA fields are set after factor verification and remain zero for a password-only
	// login.
	MFAMethod       string
	MFAVerifiedAt   *time.Time
	MFACredentialID string

	// PasswordChangeRequired issues a session restricted until the
	// password is changed (auth-internals.md §3 "Password policy at
	// sign-in").
	PasswordChangeRequired bool
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
	Persistent   bool
}

// Issue mints a new access/refresh token pair and records the refresh
// token's hash as a new sessions row (id == family_id: this is always a
// fresh family's first row, never a rotation).
func (i *Issuer) Issue(ctx context.Context, p LoginParams) (*Tokens, error) {
	return i.issue(ctx, p, i.sessions.Insert)
}

// IssueTx keeps MFA verification and session creation under the account
// lock held by the caller, so a tenant reset cannot miss the new session.
func (i *Issuer) IssueTx(ctx context.Context, tx *sql.Tx, p LoginParams) (*Tokens, error) {
	return i.issue(ctx, p, func(ctx context.Context, row session.Row) error {
		return i.sessions.InsertTx(ctx, tx, row)
	})
}

func (i *Issuer) issue(ctx context.Context, p LoginParams, insertSession func(context.Context, session.Row) error) (*Tokens, error) {
	t, err := i.tenants.GetBySlug(ctx, p.TenantSlug)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant %q: %w", p.TenantSlug, err)
	}

	if i.allowlists != nil {
		ok, err := i.allowlists.Check(ctx, t.ID, p.IPAddress)
		if err != nil {
			return nil, fmt.Errorf("check ip allowlist: %w", err)
		}

		if !ok {
			return nil, ErrIPNotAllowed
		}
	}

	roleNames, err := i.roles.RoleNamesForUser(ctx, p.TenantSlug, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("look up roles for user %s: %w", p.UserID, err)
	}

	deviceID := p.DeviceID
	if deviceID == "" {
		deviceID = uuid.New().String()
	}

	refreshToken, refreshHash, err := newRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	now := i.now()
	sessionID := uuid.New().String()
	expiresAt, err := i.expiresAt(ctx, t.ID, p.Persistent, now, now)
	if err != nil {
		return nil, fmt.Errorf("resolve session expiry: %w", err)
	}

	if err := insertSession(ctx, session.Row{
		ID:              sessionID,
		UserID:          p.UserID,
		TenantID:        t.ID,
		DeviceID:        deviceID,
		RefreshHash:     refreshHash,
		UserAgent:       p.UserAgent,
		IPAddress:       p.IPAddress,
		CountryCode:     p.CountryCode,
		ExpiresAt:       expiresAt,
		Persistent:      p.Persistent,
		MFAMethod:       p.MFAMethod,
		MFAVerifiedAt:   p.MFAVerifiedAt,
		MFACredentialID: p.MFACredentialID,

		PasswordChangeRequired: p.PasswordChangeRequired,
	}); err != nil {
		return nil, fmt.Errorf("record session: %w", err)
	}

	accessToken, expiresIn, err := i.signAccessToken(sessionID, t.ID, p.UserID, roleNames, p.MFAMethod, p.MFAVerifiedAt, p.PasswordChangeRequired, now, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	// Every new session is a sign-in to this tenant, whichever flow issued
	// it (auth-internals.md §3 step 11). A display field, so a failure
	// doesn't fail the sign-in.
	if err := i.roles.RecordLogin(ctx, p.TenantSlug, p.UserID, p.IPAddress); err != nil {
		log.Warn().Err(err).Str("user_id", p.UserID).Str("tenant", p.TenantSlug).Msg("authtoken: recording last sign-in failed")
	}

	return &Tokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
		Persistent:   p.Persistent,
	}, nil
}

// ReissueAccessToken signs current assurance and restriction claims without rotating the
// refresh token or creating a session. The caller persists assurance first; sessionEnd
// caps token expiry.
func (i *Issuer) ReissueAccessToken(sessionID, tenantID, userID string, roleNames []string, mfaMethod string, mfaVerifiedAt *time.Time, passwordChangeRequired bool, sessionEnd time.Time) (accessToken string, expiresIn int, err error) {
	accessToken, expiresIn, err = i.signAccessToken(sessionID, tenantID, userID, roleNames, mfaMethod, mfaVerifiedAt, passwordChangeRequired, i.now(), sessionEnd)
	if err != nil {
		return "", 0, fmt.Errorf("sign access token: %w", err)
	}

	return accessToken, expiresIn, nil
}

// signAccessToken always includes pwd in amr and appends a nonempty MFA method. Expiry is
// capped by the session's end; expiresIn reports seconds.
func (i *Issuer) signAccessToken(sessionID, tenantID, userID string, roleNames []string, mfaMethod string, mfaVerifiedAt *time.Time, passwordChangeRequired bool, now, sessionEnd time.Time) (token string, expiresIn int, err error) {
	exp := now.Add(accessTokenTTL)
	if !sessionEnd.IsZero() && sessionEnd.Before(exp) {
		exp = sessionEnd
	}

	amr := []string{"pwd"}
	var mfaVerifiedAtClaim *int64
	if mfaMethod != "" {
		amr = append(amr, mfaMethod)
	}

	if mfaVerifiedAt != nil {
		mfaVerifiedAtClaim = new(mfaVerifiedAt.Unix())
	}

	claims := Claims{
		Issuer:        issuerName,
		Subject:       userID,
		IssuedAt:      jwt.NewNumericDate(now),
		ExpiresAt:     jwt.NewNumericDate(exp),
		ID:            uuid.New().String(),
		SessionID:     sessionID,
		TenantID:      tenantID,
		Roles:         roleNames,
		Scope:         []string{"api"},
		AMR:           amr,
		MFAVerifiedAt: mfaVerifiedAtClaim,

		PasswordChangeRequired: passwordChangeRequired,
	}

	unsigned := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	unsigned.Header["kid"] = i.signingKey.KID
	token, err = unsigned.SignedString(i.signingKey.Private)
	if err != nil {
		return "", 0, err
	}

	return token, int(exp.Sub(now).Seconds()), nil
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

// Refresh rotates the session before signing replacement tokens. Rejections return nil
// Tokens with a non-OK outcome; failures after rotation leave the old token unusable and
// require a fresh login.
func (i *Issuer) Refresh(ctx context.Context, presentedRefreshToken string, p RefreshParams) (*Tokens, session.RotateOutcome, error) {
	presentedHash := hashRefreshToken(presentedRefreshToken)

	newToken, newHash, err := newRefreshToken()
	if err != nil {
		return nil, 0, fmt.Errorf("generate refresh token: %w", err)
	}

	newSessionID := uuid.New().String()

	now := i.now()
	newExpiresAt := func(tenantID string, persistent bool, familyStart time.Time) (time.Time, error) {
		return i.expiresAt(ctx, tenantID, persistent, now, familyStart)
	}

	result, err := i.sessions.Rotate(ctx, presentedHash, newSessionID, newHash, p.DeviceID, now, newExpiresAt, p.UserAgent, p.IPAddress, p.CountryCode)
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

	accessToken, expiresIn, err := i.signAccessToken(newSessionID, result.TenantID, result.UserID, roleNames, result.MFAMethod, result.MFAVerifiedAt, result.PasswordChangeRequired, now, result.ExpiresAt)
	if err != nil {
		return nil, 0, fmt.Errorf("sign access token: %w", err)
	}

	return &Tokens{
		AccessToken:  accessToken,
		RefreshToken: newToken,
		ExpiresIn:    expiresIn,
		Persistent:   result.Persistent,
	}, session.RotateOK, nil
}
