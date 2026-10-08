// Package permcache caches user roles in Redis and role-permission mappings in process.
// Authentication consumes both layers, and role changes invalidate cached roles.
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

// Cache expiry bounds the delay before role changes or expired assignments are reflected
// across instances.
const roleCacheTTL = 60 * time.Second

func roleCacheKey(tenantID, userID string) string {
	return fmt.Sprintf("authz:%s:%s:roles", tenantID, userID)
}

type roleCacheEntry struct {
	RoleIDs []string
}

// Get reports cache miss on Redis or decode errors so callers fall back to authoritative
// Postgres reads.
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

// Set is best-effort; failure leaves the next Get to fetch authoritative roles.
func (c *RoleCache) Set(ctx context.Context, tenantID, userID string, roleIDs []string) {
	encoded, err := msgpack.Marshal(roleCacheEntry{RoleIDs: roleIDs})
	if err != nil {
		return
	}
	_ = c.cache.SetWithTTL(ctx, roleCacheKey(tenantID, userID), string(encoded), roleCacheTTL)
}

// Invalidate removes cached roles for the tenant/user pair. Errors are returned because
// failed invalidation can serve stale permissions until TTL expiry.
func (c *RoleCache) Invalidate(ctx context.Context, tenantID, userID string) error {
	if err := c.cache.Delete(ctx, roleCacheKey(tenantID, userID)); err != nil {
		return fmt.Errorf("invalidate role cache for tenant %q user %q: %w", tenantID, userID, err)
	}
	return nil
}
