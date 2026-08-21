// Package authcheck validates a request's JWT access token and hydrates
// the auth-internals.md §9 pipeline's user/tenant/permission context — the
// portion of that 12-step pipeline buildable now (steps 6, 7's JWT branch,
// 8, 11) against authtoken/signingkey/sessionrevoke's already-landed
// issuance and revocation infrastructure (goerp#210/#217/#147).
//
// Checker.Authenticate assumes tenant resolution (goerp#89) has already
// happened — it takes the resolved tenant, it doesn't resolve one itself —
// and doesn't wire into any actual HTTP middleware chain (goerp#91).
// Permission-context hydration (§9 step 10) goes through
// internal/engine/permcache's RoleCache/RolePermissionMap rather than
// querying Postgres on every request. Authenticate also handles the erp_
// API-key branch (§7), gated behind GOERP_ENABLE_API_KEYS. The mfa_token
// branch and MFA enforcement are a separate, still-blocked ticket
// (goerp#224).
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
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
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
)

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
// this package's slice of the pipeline populates. MFAPending stays
// zero-valued — the mfa_token branch is goerp#224, not this package.
type AuthContext struct {
	IsAuthenticated bool
	UserID          string
	SessionID       string
	TenantID        string
	TenantSlug      string
	AMR             []string
	MFAVerified     bool
	MFAVerifiedAt   *time.Time
	Roles           []string // from the JWT claim — a snapshot at issuance
	RolesLive       []string // live from role.Store, may differ if roles changed since issuance
	PermissionSet   permission.PermissionBitfield
	// AuthMethod is "jwt" or "api_key" — auth-internals.md §7 step 9.
	AuthMethod string
	// APIKey is the presented key's row when AuthMethod == "api_key", nil
	// otherwise — the key itself is the request's principal (§7 step 9),
	// distinct from UserID (which may be empty for a service key).
	APIKey *apikey.APIKey
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
	}
}

// Authenticate validates rawToken against tenantID/tenantSlug (already
// resolved by tenant-resolution middleware, goerp#89) and, if valid,
// hydrates user/tenant-membership/permission context. permissions is the
// caller's current registry.RegistrySnapshot's PermissionRegistry — it's
// rebuilt on every module hot reload, not a static object this package
// can hold onto, so a future auth middleware (goerp#91) is expected to
// resolve it once per request the same way it resolves the route. It's
// used only for requiredPermissions' index lookups below — permission-set
// hydration itself resolves indexes via permcache.RolePermissionMap,
// already built against the same registry generation (see
// hydratePermissionSet). requiredPermissions is the resolved route's
// declared permission requirement, checked last (§9 step 11) so a call can
// skip the extra lookups entirely for a route that declares none. remoteIP
// is the request's already-resolved client IP (§9 step 3, "Real IP
// resolution" — upstream of token validation, not this package's job to
// (re-)implement) — used only by the erp_ API-key branch's allowed-IPs
// check.
//
// rawToken == "" is Anonymous, not an error — auth-internals.md §9 step 7:
// "No token present: Set auth = Anonymous, Continue (route may be
// public)". Every other rejection (invalid/expired/blocklisted token,
// inactive user, non-member, missing permission) returns one of this
// package's sentinel errors, distinct from Anonymous, since presenting a
// bad token isn't the same as presenting none.
func (c *Checker) Authenticate(ctx context.Context, rawToken, tenantID, tenantSlug, remoteIP string, permissions *permission.PermissionRegistry, requiredPermissions []string) (*AuthContext, error) {
	if rawToken == "" {
		return &AuthContext{IsAuthenticated: false}, nil
	}

	// erp_ prefix routes to API key validation instead of JWT parsing —
	// §9 step 7. When the flag is off there's nothing to dispatch to (per
	// auth-internals.md §7's own design note), so control falls through
	// to JWT parsing below, which fails naturally on an erp_-prefixed
	// string with ErrInvalidToken.
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

	rolesLive, err := c.roles.RoleNamesForUser(ctx, tenantSlug, claims.Subject)
	if err != nil {
		return nil, fmt.Errorf("load live roles: %w", err)
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
		t := time.Unix(*claims.MFAVerifiedAt, 0)
		mfaVerifiedAt = &t
	}

	return &AuthContext{
		IsAuthenticated: true,
		UserID:          claims.Subject,
		SessionID:       claims.SessionID,
		TenantID:        claims.TenantID,
		TenantSlug:      tenantSlug,
		AMR:             claims.AMR,
		MFAVerified:     hasMFAFactor(claims.AMR),
		MFAVerifiedAt:   mfaVerifiedAt,
		Roles:           claims.Roles,
		RolesLive:       rolesLive,
		PermissionSet:   permSet,
		AuthMethod:      "jwt",
	}, nil
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

	// Defense in depth against a leaked key from one tenant being replayed
	// against a different tenant's subdomain — same reasoning the JWT
	// branch's own tenant-mismatch check above already documents.
	if key.TenantID != tenantID {
		return nil, ErrTenantMismatch
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return nil, ErrAPIKeyExpired
	}
	if !ipAllowed(key.AllowedIPs, remoteIP) {
		return nil, ErrAPIKeyIPNotAllowed
	}

	var rolesLive []string
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

		rolesLive, err = c.roles.RoleNamesForUser(ctx, tenantSlug, *key.UserID)
		if err != nil {
			return nil, fmt.Errorf("load live roles: %w", err)
		}

		// sessionID "" is correct here — an API-key request has no
		// session, so IsRolesStale's check against an empty suffix
		// always reads "not stale," which is the right answer since
		// there's no session to ever mark stale.
		permSet, err = c.hydratePermissionSet(ctx, tenantID, tenantSlug, *key.UserID, "")
		if err != nil {
			return nil, fmt.Errorf("hydrate permission set: %w", err)
		}
		permSet.And(scopesToBitfield(key.Scopes, permissions))
	} else {
		permSet = scopesToBitfield(key.Scopes, permissions)
	}

	// §7 step 10 — updated once the auth context itself is built,
	// regardless of whether step 11's permission check below then denies
	// this particular request: the key was still genuinely presented and
	// authenticated. Fire-and-forget: context.Background(), not ctx,
	// since this update must outlive the request that triggered it, which
	// may already be done (and its ctx canceled) by the time this
	// goroutine runs.
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
		TenantID:        key.TenantID,
		TenantSlug:      tenantSlug,
		RolesLive:       rolesLive,
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

// hydratePermissionSet builds userID's permission bitfield for tenantSlug,
// per auth-internals.md §14's cache layers: it prefers permcache.RoleCache
// (layer 2, Redis, 60s TTL) over a Postgres round trip, resolving each
// cached role ID through permcache.RolePermissionMap (layer 3, in-process).
// A role ID RolePermissionMap doesn't currently resolve (e.g. a role
// created since the map's last rebuild) is skipped rather than treated as
// an error — the same log-and-skip failure isolation
// internal/engine/permcache's own buildTenantRoles/resolveRoleBitfield use
// for the identical situation.
//
// sessionrevoke.Revoker.IsRolesStale is checked first: a session marked
// stale by a future role-grant/revoke flow (§14 "Cache invalidation on
// role change" step 3) bypasses RoleCache entirely and re-reads from
// Postgres, since the cached entry — and the JWT's own roles claim — may
// no longer reflect the live grant. A staleness-check error fails open
// (treated as not-stale) — the same convention RoleCache.Get already
// applies to its own Redis errors, and consistent with §14's documented
// "Role cache / permission set: fail over to the authoritative source"
// behavior for the cache layers themselves.
//
// No PermissionRegistry parameter is needed here — RolePermissionMap's
// bitfields are already resolved against index assignments at RebuildAll
// time, and engine.go rebuilds it in lockstep with every registry update
// (its own doc comment), so a lookup by role ID needs no further
// name-to-index resolution the way the old direct-Postgres path did.
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

// hasMFAFactor reports whether amr contains an MFA factor type, per
// auth-internals.md §4/§9 — always false until goerp#224's MFA cluster
// lands, since authtoken.Issue only ever sets AMR to ["pwd"] today.
func hasMFAFactor(amr []string) bool {
	for _, m := range amr {
		switch m {
		case "totp", "webauthn", "recovery_code":
			return true
		}
	}
	return false
}

// keyFunc returns the public key to verify token against. Only the Active
// key exists today (no rotation/Previous list yet, backlog #256) — a kid
// that doesn't match it is simply unrecognized, not looked up further.
func (c *Checker) keyFunc(token *jwt.Token) (any, error) {
	kid, ok := token.Header["kid"].(string)
	if !ok || kid != c.signingKey.KID {
		return nil, fmt.Errorf("unrecognized signing key kid %v", token.Header["kid"])
	}
	return c.signingKey.Public, nil
}
