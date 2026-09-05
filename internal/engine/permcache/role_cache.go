// Package permcache implements the two permission-caching layers
// auth-internals.md §14 describes for populating a request's permission
// bitfield without hitting Postgres every time: RoleCache (§14 layer 2,
// Redis, 60s TTL) and RolePermissionMap (§14 layer 3, in-process, rebuilt
// on module load/hot-reload). Both are consumed by
// internal/engine/authcheck.Checker's permission-context hydration step.
// RoleCache.Invalidate is called by internal/engine/auth/roleassign's
// grant/revoke flow (goerp#619).
package permcache

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/vmihailenco/msgpack/v5"
)

// RoleCache is auth-internals.md §14's cache layer 2 — the tenant's
// user_roles grants for one user, cached in Redis so
// internal/engine/authcheck.Checker's hydration step doesn't query
// Postgres on every request.
type RoleCache struct {
	cache *cache.Client
}

func NewRoleCache(c *cache.Client) *RoleCache {
	return &RoleCache{cache: c}
}

// roleCacheTTL matches §14 layer 2's documented 60-second TTL — the
// maximum lag before any role change (grant, revoke, or an assignment
// aging past its expires_at) is reflected everywhere.
const roleCacheTTL = 60 * time.Second

func roleCacheKey(tenantID, userID string) string {
	return fmt.Sprintf("authz:%s:%s:roles", tenantID, userID)
}

type roleCacheEntry struct {
	RoleIDs []string
}

// Get reads back the cached role IDs for (tenantID, userID). found is
// false for a genuine cache miss or any Redis/decode failure — Get fails
// open, same convention internal/engine/tenantresolve's Redis cache
// already uses: a cache-population problem falls through to the caller's
// own Postgres query (role.Store.RoleIDsForUser) rather than surfacing an
// error the caller has no better fallback for anyway.
func (c *RoleCache) Get(ctx context.Context, tenantID, userID string) (roleIDs []string, found bool) {
	cached, ok, err := c.cache.Get(ctx, roleCacheKey(tenantID, userID))
	if err != nil || !ok {
		return nil, false
	}

	var entry roleCacheEntry
	if err := msgpack.Unmarshal([]byte(cached), &entry); err != nil {
		return nil, false
	}

	return entry.RoleIDs, true
}

// Set populates the cache. Best-effort — a write failure just means the
// next Get is a miss, so it's silently ignored rather than surfaced to a
// caller that has no useful recovery for it anyway.
func (c *RoleCache) Set(ctx context.Context, tenantID, userID string, roleIDs []string) {
	encoded, err := msgpack.Marshal(roleCacheEntry{RoleIDs: roleIDs})
	if err != nil {
		return
	}
	_ = c.cache.SetWithTTL(ctx, roleCacheKey(tenantID, userID), string(encoded), roleCacheTTL)
}

// Invalidate deletes the cached entry for (tenantID, userID) —
// auth-internals.md §14 "Cache invalidation on role change" step 1, called
// synchronously by internal/engine/auth/roleassign's grant/revoke flow
// (goerp#619). Unlike
// Get/Set, the error is returned rather than swallowed: a failed
// invalidation on a real grant/revoke path means a stale entry may keep
// serving for up to the remaining TTL, a correctness-relevant signal the
// caller should be able to act on (retry, log, alert) rather than one
// silently lost.
func (c *RoleCache) Invalidate(ctx context.Context, tenantID, userID string) error {
	if err := c.cache.Delete(ctx, roleCacheKey(tenantID, userID)); err != nil {
		return fmt.Errorf("invalidate role cache for tenant %q user %q: %w", tenantID, userID, err)
	}
	return nil
}
