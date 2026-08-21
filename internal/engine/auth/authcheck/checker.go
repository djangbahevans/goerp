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
// querying Postgres on every request. The mfa_token and erp_ API-key
// branches, and MFA enforcement, are separate, still-blocked tickets
// (goerp#223/#224).
package authcheck

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken     = errors.New("invalid access token")
	ErrSessionRevoked   = errors.New("session revoked")
	ErrTenantMismatch   = errors.New("token was not issued for this tenant")
	ErrUserNotActive    = errors.New("user is not active")
	ErrNotTenantMember  = errors.New("user is not a member of this tenant")
	ErrPermissionDenied = errors.New("missing required permission")
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
// this package's slice of the pipeline populates. APIKey and MFAPending
// stay zero-valued — the API-key and mfa_token branches are goerp#223/
// #224, not this package.
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
}

type Checker struct {
	signingKey *signingkey.SigningKey
	revoker    *sessionrevoke.Revoker
	users      *user.Store
	roles      *role.Store
	roleCache  *permcache.RoleCache
	roleMap    *permcache.RolePermissionMap
}

func NewChecker(
	signingKey *signingkey.SigningKey,
	revoker *sessionrevoke.Revoker,
	users *user.Store,
	roles *role.Store,
	roleCache *permcache.RoleCache,
	roleMap *permcache.RolePermissionMap,
) *Checker {
	return &Checker{
		signingKey: signingKey,
		revoker:    revoker,
		users:      users,
		roles:      roles,
		roleCache:  roleCache,
		roleMap:    roleMap,
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
// skip the extra lookups entirely for a route that declares none.
//
// rawToken == "" is Anonymous, not an error — auth-internals.md §9 step 7:
// "No token present: Set auth = Anonymous, Continue (route may be
// public)". Every other rejection (invalid/expired/blocklisted token,
// inactive user, non-member, missing permission) returns one of this
// package's sentinel errors, distinct from Anonymous, since presenting a
// bad token isn't the same as presenting none.
func (c *Checker) Authenticate(ctx context.Context, rawToken, tenantID, tenantSlug string, permissions *permission.PermissionRegistry, requiredPermissions []string) (*AuthContext, error) {
	if rawToken == "" {
		return &AuthContext{IsAuthenticated: false}, nil
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
	}, nil
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
