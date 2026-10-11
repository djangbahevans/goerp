package tenantconfig

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// Expiration bounds stale reads when an instance misses a Postgres notification.
const cacheTTL = 5 * time.Minute

// JSON scalar extraction aligns tenant values with plain-text overrides and defaults.
const resolveConfigQuery = `
SELECT value, encrypted FROM (
    SELECT 1 AS priority, value, false AS encrypted
    FROM system.tenant_config_overrides WHERE tenant_id = $1 AND key = $2
    UNION ALL
    SELECT 2, value #>> '{}', encrypted
    FROM %s.module_config WHERE module_name = $3 AND key = $4 AND value #>> '{}' IS NOT NULL
    UNION ALL
    SELECT 3, $5::text, false WHERE $5::text IS NOT NULL
) tiers
ORDER BY priority
LIMIT 1
`

type cachedValue struct {
	value     string
	encrypted bool
	found     bool
	expiresAt time.Time
}

type Resolver struct {
	store    *Store
	tenants  *tenant.Store
	registry *registry.ModuleRegistry

	mu         sync.Mutex
	cache      map[string]cachedValue
	generation uint64
}

func NewResolver(store *Store, tenants *tenant.Store, reg *registry.ModuleRegistry) *Resolver {
	return &Resolver{
		store:    store,
		tenants:  tenants,
		registry: reg,
		cache:    map[string]cachedValue{},
	}
}

// Get accepts a qualified key. Only encrypted tenant rows return ciphertext;
// a missing value returns found=false without an error.
func (r *Resolver) Get(ctx context.Context, tenantID, key string) (value string, encrypted, found bool, err error) {
	cacheKey := configCacheKey(tenantID, key)
	if cached, ok := r.cachedGet(cacheKey); ok {
		return cached.value, cached.encrypted, cached.found, nil
	}
	gen := r.currentGeneration()

	t, err := r.tenants.GetByID(ctx, tenantID)
	if err != nil {
		return "", false, false, fmt.Errorf("resolve tenant for config lookup: %w", err)
	}

	moduleName, subKey, _ := strings.Cut(key, ".")
	query := fmt.Sprintf(resolveConfigQuery, tenantschema.Name(t.Slug))

	row := r.store.db.QueryRowContext(ctx, query, tenantID, key, moduleName, subKey, r.manifestDefault(moduleName, subKey))
	switch err := row.Scan(&value, &encrypted); {
	case errors.Is(err, sql.ErrNoRows):
		value, encrypted, found = "", false, false
	case err != nil:
		return "", false, false, fmt.Errorf("resolve tenant config %q: %w", key, err)
	default:
		found = true
	}

	r.cacheSetIfFresh(cacheKey, cachedValue{value: value, encrypted: encrypted, found: found}, gen)
	return value, encrypted, found, nil
}

// SQL scalar extraction leaves strings bare and preserves JSON for other values.
func (r *Resolver) manifestDefault(moduleName, subKey string) any {
	snap := r.registry.Snapshot()
	if snap == nil {
		return nil
	}
	mod, ok := snap.Modules()[moduleName]
	if !ok || mod.Status == module.StatusFailed {
		return nil
	}
	v, ok := mod.Manifest.TenantConfigSeeds[moduleName+"."+subKey]
	if !ok {
		return nil
	}
	if s, ok := v.(string); ok {
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(data)
}

func (r *Resolver) cachedGet(cacheKey string) (cachedValue, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[cacheKey]
	if !ok {
		return cachedValue{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(r.cache, cacheKey)
		return cachedValue{}, false
	}
	return entry, true
}

func (r *Resolver) currentGeneration() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.generation
}

// Invalidation during a database read prevents that stale result from reentering the cache.
func (r *Resolver) cacheSetIfFresh(cacheKey string, entry cachedValue, gen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generation != gen {
		return
	}
	entry.expiresAt = time.Now().Add(cacheTTL)
	r.cache[cacheKey] = entry
}

func (r *Resolver) Invalidate(tenantID, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, configCacheKey(tenantID, key))
	r.generation++
}

func configCacheKey(tenantID, key string) string {
	return "config:" + tenantID + ":" + key
}
