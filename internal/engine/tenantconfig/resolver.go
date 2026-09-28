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

// cacheTTL is the resolved-value in-memory cache's lifetime —
// multitenancy-internals.md §7's own 5-minute figure — and so also the
// worst-case propagation delay for an instance a Listener never reaches
// (e.g. its LISTEN connection was down when configChangedChannel fired).
const cacheTTL = 5 * time.Minute

// resolveConfigQuery picks the highest-priority of the three sources
// multitenancy-internals.md §7's "Config resolution order" documents: an
// operator override, then a tenant-admin-set module_config value, then
// the manifest's own default. module_config's JSONB value is unwrapped
// with `#>> '{}'` to a plain scalar's text so it lines up with
// tenant_config_overrides' own plain-text value column. encrypted is
// module_config's own flag; the other two tiers are always plaintext.
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

// Resolver implements getConfig — multitenancy-internals.md §7's
// three-tier config resolution chain — in front of an in-memory,
// cacheTTL-lived cache.
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

// Get resolves tenantID's effective value for key — already fully
// namespaced ({module}.{key}), same convention Store's own doc comment
// describes. encrypted reports whether value is ciphertext, which is true
// only for a module_config row stored encrypted. found is false, with a
// nil error, when none of the three sources has a value for key.
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

// manifestDefault returns moduleName's declared TenantConfigSeeds[subKey],
// rendered as plain text the same way resolveConfigQuery's own `#>> '{}'`
// unwraps a JSONB scalar: a string value comes back bare (unquoted), and
// everything else (number, bool, array, object) comes back as its JSON
// text form — `#>>'{}'` only strips the surrounding quotes of a string
// scalar, an array or object's brackets/braces stay put. Returns nil (SQL
// NULL, which drops the query's lowest-priority tier) when the
// module isn't loaded, failed to load, or declares no such key.
func (r *Resolver) manifestDefault(moduleName, subKey string) any {
	snap := r.registry.Snapshot()
	if snap == nil {
		return nil
	}
	mod, ok := snap.Modules()[moduleName]
	if !ok || mod.Status == module.StatusFailed {
		return nil
	}
	v, ok := mod.Manifest.TenantConfigSeeds[subKey]
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

// cachedGet evicts cacheKey on a stale hit rather than merely ignoring
// it, so a key resolved once and never looked up again doesn't sit in
// the map for the rest of the process's life.
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

// cacheSetIfFresh caches entry under cacheKey, unless generation has
// advanced past gen — an Invalidate call landed while this read was still
// in flight, meaning the value just read may already be stale. Skipping
// the cache write in that case is safe: the next Get simply re-reads,
// rather than risking caching a value Invalidate's own notification was
// specifically trying to evict.
func (r *Resolver) cacheSetIfFresh(cacheKey string, entry cachedValue, gen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generation != gen {
		return
	}
	entry.expiresAt = time.Now().Add(cacheTTL)
	r.cache[cacheKey] = entry
}

// Invalidate drops tenantID/key's cached entry, if any, and advances the
// generation counter cacheSetIfFresh checks — a Listener's own reaction
// to a configChangedChannel notification naming this pair.
func (r *Resolver) Invalidate(tenantID, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, configCacheKey(tenantID, key))
	r.generation++
}

func configCacheKey(tenantID, key string) string {
	return "config:" + tenantID + ":" + key
}
