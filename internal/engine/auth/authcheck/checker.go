// Package authcheck validates JWT, API-key and MFA credentials and hydrates tenant
// membership and permissions for an already-resolved tenant.
package authcheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
)

var (
	ErrInvalidToken       = errors.New("invalid access token")
	ErrSessionRevoked     = errors.New("session revoked")
	ErrTenantMismatch     = errors.New("token was not issued for this tenant")
	ErrUserNotActive      = errors.New("user is not active")
	ErrNotTenantMember    = errors.New("user is not a member of this tenant")
	ErrPermissionDenied   = errors.New("missing required permission")
	ErrAPIKeyInvalid      = errors.New("invalid api key")
	ErrAPIKeyExpired      = errors.New("api key expired")
	ErrAPIKeyIPNotAllowed = errors.New("api key not allowed from this ip")
	// ErrPasswordChangeRequired rejects a session restricted until its
	// password is changed (auth-internals.md §3 "Password policy at
	// sign-in") on a route that isn't one of the password-fix routes.
	ErrPasswordChangeRequired = errors.New("password change required")
)

// ErrMFATokenTenantMismatch mirrors ErrTenantMismatch for the mfa_token
// branch — a token issued for one tenant presented against a different
// tenant's Host-resolved subdomain.
var ErrMFATokenTenantMismatch = errors.New("mfa_token was not issued for this tenant")

const accessTokenCookieName = "__Host-access_token"

// ExtractToken returns the bearer token from r — the Authorization header
// if present, else the access token cookie, matching auth-internals.md §9
// step 6's precedence. Returns "" if neither is present.
func ExtractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		return strings.TrimPrefix(auth, "Bearer ")
	}

	if cookie, err := r.Cookie(accessTokenCookieName); err == nil {
		return cookie.Value
	}

	return ""
}

// AuthContext is the auth-internals.md §9 "Auth context object" fields
// this package's slice of the pipeline populates. MFAPending is set by
// AuthenticateMFAToken's step 7 third branch — every other branch leaves
// it false.
type AuthContext struct {
	IsAuthenticated bool
	UserID          string
	ContactID       string
	SessionID       string
	TenantID        string
	TenantSlug      string
	AMR             []string
	MFAVerified     bool
	MFAVerifiedAt   *time.Time
	Roles           []string // from the JWT claim — a snapshot at issuance
	RolesLive       []string // live from role.Store, may differ if roles changed since issuance
	PermissionSet   permission.PermissionBitfield
	AuthMethod      string
	// APIKey is the presented key's row when AuthMethod == "api_key", nil
	// otherwise — the key itself is the request's principal (§7 step 9),
	// distinct from UserID (which may be empty for a service key).
	APIKey *apikey.APIKey
	// MFAPending is true when AuthenticateMFAToken accepted a valid
	// mfa_token — the caller is mid-login, not yet a fully authenticated
	// principal (IsAuthenticated stays false in that case).
	MFAPending bool
	// PasswordChangeRequired is the session's pcr claim. Only
	// AuthenticateAllowingPasswordChange returns a context with it set;
	// Authenticate refuses such a session.
	PasswordChangeRequired bool
}

type Checker struct {
	signingKey    *signingkey.SigningKey
	revoker       *sessionrevoke.Revoker
	users         *user.Store
	roles         *role.Store
	roleCache     *permcache.RoleCache
	roleMap       *permcache.RolePermissionMap
	apiKeys       *apikey.Store
	enableAPIKeys bool
	mfaTokens     *mfatoken.Codec
	mfaCreds      *mfa.Store
	mfaPolicies   *enforce.Store
}

func NewChecker(
	signingKey *signingkey.SigningKey,
	revoker *sessionrevoke.Revoker,
	users *user.Store,
	roles *role.Store,
	roleCache *permcache.RoleCache,
	roleMap *permcache.RolePermissionMap,
	apiKeys *apikey.Store,
	enableAPIKeys bool,
	mfaTokens *mfatoken.Codec,
	mfaCreds *mfa.Store,
	mfaPolicies *enforce.Store,
) *Checker {
	return &Checker{
		signingKey:    signingKey,
		revoker:       revoker,
		users:         users,
		roles:         roles,
		roleCache:     roleCache,
		roleMap:       roleMap,
		apiKeys:       apiKeys,
		enableAPIKeys: enableAPIKeys,
		mfaTokens:     mfaTokens,
		mfaCreds:      mfaCreds,
		mfaPolicies:   mfaPolicies,
	}
}

// Authenticate validates rawToken for the resolved tenant and checks requiredPermissions
// against the current registry snapshot. An empty token is anonymous; invalid credentials
// return sentinel errors. Password-restricted sessions require
// AuthenticateAllowingPasswordChange.
func (c *Checker) Authenticate(ctx context.Context, rawToken, tenantID, tenantSlug, remoteIP string, permissions *permission.PermissionRegistry, requiredPermissions []string) (*AuthContext, error) {
	return c.authenticate(ctx, rawToken, tenantID, tenantSlug, remoteIP, permissions, requiredPermissions, false)
}

// AuthenticateAllowingPasswordChange is Authenticate for the routes a
// session restricted by password_change_required may still reach
// (auth-internals.md §3 "Password policy at sign-in"): GET /auth/me,
// POST /auth/me/change-password, POST /auth/logout and the MFA routes §9
// step 9 exempts. The returned context reports the restriction in
// PasswordChangeRequired.
func (c *Checker) AuthenticateAllowingPasswordChange(ctx context.Context, rawToken, tenantID, tenantSlug, remoteIP string, permissions *permission.PermissionRegistry, requiredPermissions []string) (*AuthContext, error) {
	return c.authenticate(ctx, rawToken, tenantID, tenantSlug, remoteIP, permissions, requiredPermissions, true)
}

func (c *Checker) authenticate(ctx context.Context, rawToken, tenantID, tenantSlug, remoteIP string, permissions *permission.PermissionRegistry, requiredPermissions []string, allowPasswordChange bool) (*AuthContext, error) {
	if rawToken == "" {
		return &AuthContext{IsAuthenticated: false}, nil
	}

	// API keys use their validator only when enabled. Otherwise JWT parsing rejects the
	// erp_ token.
	if c.enableAPIKeys && strings.HasPrefix(rawToken, "erp_") {
		return c.authenticateAPIKey(ctx, rawToken, tenantID, tenantSlug, remoteIP, permissions, requiredPermissions)
	}

	claims := &authtoken.Claims{}
	_, err := jwt.ParseWithClaims(rawToken, claims, c.keyFunc, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer("goerp"))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	if claims.TenantID != tenantID {
		return nil, ErrTenantMismatch
	}

	blocked, err := c.revoker.IsBlocked(ctx, claims.SessionID)
	if err != nil {
		return nil, fmt.Errorf("check session blocklist: %w", err)
	}

	if blocked {
		return nil, ErrSessionRevoked
	}

	u, err := c.users.GetByID(ctx, claims.Subject)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			return nil, ErrUserNotActive
		}

		return nil, fmt.Errorf("load user: %w", err)
	}

	if u.Status != user.StatusActive {
		return nil, ErrUserNotActive
	}

	isMember, err := c.roles.IsMember(ctx, tenantSlug, claims.Subject)
	if err != nil {
		return nil, fmt.Errorf("check tenant membership: %w", err)
	}

	if !isMember {
		return nil, ErrNotTenantMember
	}

	// Ahead of the permission check, so a restricted session is sent to the
	// change-password page rather than told it lacks a permission.
	if claims.PasswordChangeRequired && !allowPasswordChange {
		return nil, ErrPasswordChangeRequired
	}

	actor, err := c.roles.ResolveActingUser(ctx, tenantSlug, claims.Subject)
	if err != nil {
		if errors.Is(err, role.ErrNotMember) {
			return nil, ErrNotTenantMember
		}

		return nil, fmt.Errorf("resolve acting user: %w", err)
	}

	permSet, err := c.hydratePermissionSet(ctx, claims.TenantID, tenantSlug, claims.Subject, claims.SessionID)
	if err != nil {
		return nil, fmt.Errorf("hydrate permission set: %w", err)
	}

	for _, required := range requiredPermissions {
		idx, ok := permissions.Index(required)
		if !ok || !permSet.Has(idx) {
			return nil, fmt.Errorf("%w: %s", ErrPermissionDenied, required)
		}
	}

	var mfaVerifiedAt *time.Time
	if claims.MFAVerifiedAt != nil {
		mfaVerifiedAt = new(time.Unix(*claims.MFAVerifiedAt, 0))
	}

	return &AuthContext{
		IsAuthenticated: true,
		UserID:          claims.Subject,
		ContactID:       actor.ContactID,
		SessionID:       claims.SessionID,
		TenantID:        claims.TenantID,
		TenantSlug:      tenantSlug,
		AMR:             claims.AMR,
		MFAVerified:     hasMFAFactor(claims.AMR),
		MFAVerifiedAt:   mfaVerifiedAt,
		Roles:           claims.Roles,
		RolesLive:       actor.Roles,
		PermissionSet:   permSet,
		AuthMethod:      "jwt",

		PasswordChangeRequired: claims.PasswordChangeRequired,
	}, nil
}

// AuthenticateMFAToken verifies signature, expiry, purpose and tenant binding. It returns
// a pending principal; the verify handler owns token consumption, Origin checks and
// attempt lockout.
func (c *Checker) AuthenticateMFAToken(rawToken, tenantID string) (*AuthContext, error) {
	claims, err := c.mfaTokens.Verify(rawToken)
	if err != nil {
		return nil, err
	}

	if claims.TenantID != tenantID {
		return nil, ErrMFATokenTenantMismatch
	}

	return &AuthContext{
		IsAuthenticated: false,
		MFAPending:      true,
		UserID:          claims.Subject,
		TenantID:        claims.TenantID,
	}, nil
}

// EnforceMFA implements auth-internals.md §8/§9 step 9's MFA enforcement
// decision for an already-JWT-authenticated request (authCtx, as returned
// by Authenticate's JWT branch). path is the resolved route's path, used
// only for enforce.RouteExempt's fixed-path exemption check — a request to
// an exempt route (mfa enroll/verify/reverify) short-circuits to Allowed
// without loading policy or querying enrollment, since those routes either
// carry no full session yet or are the handler that resolves a prior
// ReverifyRequired/FactorRequired/SetupRequired decision.
func (c *Checker) EnforceMFA(ctx context.Context, path, tenantID string, authCtx *AuthContext) (enforce.Decision, error) {
	if enforce.RouteExempt(path) {
		return enforce.Allowed, nil
	}

	policy, err := c.mfaPolicies.LoadPolicy(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("load mfa policy: %w", err)
	}

	creds, err := c.mfaCreds.ListAccepted(ctx, authCtx.UserID, mfa.Scope{TenantID: tenantID})
	if err != nil {
		return "", fmt.Errorf("load mfa credentials: %w", err)
	}

	evalCtx := enforce.Context{
		UserRoles:     authCtx.RolesLive,
		Enrolled:      mfa.HasFactor(creds),
		AMRHasFactor:  hasMFAFactor(authCtx.AMR),
		MFAVerifiedAt: authCtx.MFAVerifiedAt,
	}

	return enforce.Evaluate(policy, evalCtx, time.Now()), nil
}

// StepUpDecision evaluates an already-enrolled user's session as if the
// tenant's MFA policy applied to them, whatever its mode: FactorRequired
// when the session never completed MFA, ReverifyRequired once its assurance
// is older than the policy's max age. Adding a factor to an enrolled
// account needs this, so a stolen password-only or stale session can't
// enroll its own authenticator (auth-internals.md §8 "MFA enrollment").
func (c *Checker) StepUpDecision(ctx context.Context, tenantID string, authCtx *AuthContext) (enforce.Decision, error) {
	policy := enforce.Policy{Mode: enforce.ModeRequired, MaxAssuranceAge: enforce.DefaultMaxAssuranceAge}
	if c.mfaPolicies != nil {
		tenantPolicy, err := c.mfaPolicies.LoadPolicy(ctx, tenantID)
		if err != nil {
			return "", fmt.Errorf("load mfa policy: %w", err)
		}

		policy.MaxAssuranceAge = tenantPolicy.MaxAssuranceAge
	}

	return enforce.Evaluate(policy, enforce.Context{
		UserRoles:     authCtx.RolesLive,
		Enrolled:      true,
		AMRHasFactor:  hasMFAFactor(authCtx.AMR),
		MFAVerifiedAt: authCtx.MFAVerifiedAt,
	}, time.Now()), nil
}

// MFASetupRequired reports whether step 9 would return mfa_setup_required
// for authCtx on a non-exempt route (auth-internals.md §8 "MFA
// enrollment"), for GET /auth/me's mfa_setup_required flag.
func (c *Checker) MFASetupRequired(ctx context.Context, tenantID string, authCtx *AuthContext) (bool, error) {
	// A Checker built without MFA stores enforces no policy.
	if c.mfaPolicies == nil || c.mfaCreds == nil {
		return false, nil
	}

	policy, err := c.mfaPolicies.LoadPolicy(ctx, tenantID)
	if err != nil {
		return false, fmt.Errorf("load mfa policy: %w", err)
	}

	if !policy.Applies(authCtx.RolesLive) {
		return false, nil
	}

	creds, err := c.mfaCreds.ListAccepted(ctx, authCtx.UserID, mfa.Scope{TenantID: tenantID})
	if err != nil {
		return false, fmt.Errorf("load mfa credentials: %w", err)
	}

	return !mfa.HasFactor(creds), nil
}

// authenticateAPIKey validates an erp_-prefixed rawToken per
// auth-internals.md §7's key authentication flow. If key.UserID is set,
// the resulting PermissionSet is the user's normal RBAC set restricted
// (never expanded) by the key's own scopes; a service key (UserID nil)
// uses only its scopes, with no RBAC role evaluation at all (§7 "Scope
// restriction").
func (c *Checker) authenticateAPIKey(ctx context.Context, rawToken, tenantID, tenantSlug, remoteIP string, permissions *permission.PermissionRegistry, requiredPermissions []string) (*AuthContext, error) {
	key, err := c.apiKeys.LookupByHash(ctx, rawToken)
	if err != nil {
		if errors.Is(err, apikey.ErrAPIKeyNotFound) {
			return nil, ErrAPIKeyInvalid
		}

		return nil, fmt.Errorf("look up api key: %w", err)
	}

	if key.TenantID != tenantID {
		return nil, ErrTenantMismatch
	}

	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return nil, ErrAPIKeyExpired
	}

	if !ipAllowed(key.AllowedIPs, remoteIP) {
		return nil, ErrAPIKeyIPNotAllowed
	}

	var actor role.ActingUser
	var permSet permission.PermissionBitfield
	if key.UserID != nil {
		u, err := c.users.GetByID(ctx, *key.UserID)
		if err != nil {
			if errors.Is(err, user.ErrUserNotFound) {
				return nil, ErrUserNotActive
			}

			return nil, fmt.Errorf("load user: %w", err)
		}

		if u.Status != user.StatusActive {
			return nil, ErrUserNotActive
		}

		isMember, err := c.roles.IsMember(ctx, tenantSlug, *key.UserID)
		if err != nil {
			return nil, fmt.Errorf("check tenant membership: %w", err)
		}

		if !isMember {
			return nil, ErrNotTenantMember
		}

		actor, err = c.roles.ResolveActingUser(ctx, tenantSlug, *key.UserID)
		if err != nil {
			if errors.Is(err, role.ErrNotMember) {
				return nil, ErrNotTenantMember
			}

			return nil, fmt.Errorf("resolve acting user: %w", err)
		}

		permSet, err = c.hydratePermissionSet(ctx, tenantID, tenantSlug, *key.UserID, "")
		if err != nil {
			return nil, fmt.Errorf("hydrate permission set: %w", err)
		}

		permSet.And(scopesToBitfield(key.Scopes, permissions))
	} else {
		permSet = scopesToBitfield(key.Scopes, permissions)
	}

	// Record usage even for permission-denied requests, outside the request's
	// cancellation scope so returning a response cannot abort the update.
	go func() {
		if err := c.apiKeys.UpdateLastUsed(context.Background(), key.ID, remoteIP); err != nil {
			log.Warn().Err(err).Str("api_key_id", key.ID).Msg("authcheck: failed to update api key last-used")
		}
	}()

	for _, required := range requiredPermissions {
		idx, ok := permissions.Index(required)
		if !ok || !permSet.Has(idx) {
			return nil, fmt.Errorf("%w: %s", ErrPermissionDenied, required)
		}
	}

	userID := ""
	if key.UserID != nil {
		userID = *key.UserID
	}

	return &AuthContext{
		IsAuthenticated: true,
		UserID:          userID,
		ContactID:       actor.ContactID,
		TenantID:        key.TenantID,
		TenantSlug:      tenantSlug,
		RolesLive:       actor.Roles,
		PermissionSet:   permSet,
		AuthMethod:      "api_key",
		APIKey:          key,
	}, nil
}

// ipAllowed reports whether remoteIP satisfies allowed — a nil/empty
// allowed list always passes (any IP allowed, per the api_keys schema's
// own semantics). An unparseable or empty remoteIP against a restricted
// key fails closed rather than silently passing.
func ipAllowed(allowed []string, remoteIP string) bool {
	if len(allowed) == 0 {
		return true
	}

	ip := net.ParseIP(remoteIP)
	if ip == nil {
		return false
	}

	for _, a := range allowed {
		if strings.Contains(a, "/") {
			if _, cidr, err := net.ParseCIDR(a); err == nil && cidr.Contains(ip) {
				return true
			}

			continue
		}

		if candidate := net.ParseIP(a); candidate != nil && candidate.Equal(ip) {
			return true
		}
	}

	return false
}

// scopesToBitfield resolves scopes (permission names) into a bitfield
// against reg. A scope name reg doesn't currently recognize is logged and
// skipped, not treated as an error — same log-and-skip convention
// permcache.RolePermissionMap's own resolveRoleBitfield uses for an
// unknown permission name.
func scopesToBitfield(scopes []string, reg *permission.PermissionRegistry) permission.PermissionBitfield {
	var bits permission.PermissionBitfield
	for _, scope := range scopes {
		idx, ok := reg.Index(scope)
		if !ok {
			log.Warn().Str("scope", scope).Msg("authcheck: unknown api key scope, skipping")
			continue
		}

		bits.Set(idx)
	}

	return bits
}

// hydratePermissionSet prefers cached role IDs and resolves their bitfields against the
// current role map. Stale sessions bypass Redis role entries; unknown roles are skipped.
func (c *Checker) hydratePermissionSet(ctx context.Context, tenantID, tenantSlug, userID, sessionID string) (permission.PermissionBitfield, error) {
	stale, err := c.revoker.IsRolesStale(ctx, sessionID)
	if err != nil {
		stale = false
	}

	var roleIDs []string
	var found bool
	if !stale {
		roleIDs, found = c.roleCache.Get(ctx, tenantID, userID)
	}

	if !found {
		roleIDs, err = c.roles.RoleIDsForUser(ctx, tenantSlug, userID)
		if err != nil {
			return nil, fmt.Errorf("load role ids: %w", err)
		}

		c.roleCache.Set(ctx, tenantID, userID, roleIDs)
	}

	var bits permission.PermissionBitfield
	for _, roleID := range roleIDs {
		if roleBits, ok := c.roleMap.Lookup(roleID); ok {
			bits.Or(roleBits)
		}
	}

	return bits, nil
}

func hasMFAFactor(amr []string) bool {
	for _, m := range amr {
		switch m {
		case "totp", "webauthn", "recovery_code":
			return true
		}
	}

	return false
}

// keyFunc rejects unrecognized key IDs; it verifies only against the active signing key.
func (c *Checker) keyFunc(token *jwt.Token) (any, error) {
	kid, ok := token.Header["kid"].(string)
	if !ok || kid != c.signingKey.KID {
		return nil, fmt.Errorf("unrecognized signing key kid %v", token.Header["kid"])
	}

	return c.signingKey.Public, nil
}

// WritePasswordChangeRequired writes 403 password_change_required and
// reports true when err is ErrPasswordChangeRequired, so a handler that
// answers every other Authenticate error with 401 can send a restricted
// session to the change-password page instead of signing it out.
func WritePasswordChangeRequired(ctx context.Context, w http.ResponseWriter, err error) bool {
	if !errors.Is(err, ErrPasswordChangeRequired) {
		return false
	}

	httperr.Write(ctx, w, http.StatusForbidden, "password_change_required", "change your password to continue")
	return true
}
