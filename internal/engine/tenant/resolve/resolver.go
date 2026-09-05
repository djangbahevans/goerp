// Package tenantresolve implements Class A tenant resolution —
// multitenancy-internals.md §4's "Resolution by route class" — for every
// ordinary module route and every already-authenticated /auth/* route.
// Class A resolves from the Host header alone, cached in Redis; the
// X-Tenant-ID/X-Tenant-Slug header and ?tenant= query-param sources
// belong to Class B/C's own anonymous, pre-session route handlers
// (auth-internals.md §9), not this package.
//
// Resolver doesn't wire into any actual HTTP middleware chain (goerp#91).
// ResolveByHost does load entitlements — TenantContext.Entitlements is a
// real, Redis-cached EntitlementSet built from the tenant's plan and any
// active per-tenant overrides (multitenancy-internals.md §4
// "Entitlement loading").
package tenantresolve

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/rs/zerolog/log"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	// ErrTenantNotFound covers both an unresolved domain and a deleted
	// tenant — callers must not distinguish between the two in their
	// response (multitenancy-internals.md §4: never reveal via a
	// different response whether a tenant exists at all).
	ErrTenantNotFound = errors.New("tenant not found")
	// ErrTenantSuspended is returned only for a resolved, non-deleted
	// tenant whose Status is "suspended".
	ErrTenantSuspended = errors.New("tenant suspended")
)

const (
	domainCacheKeyPrefix = "tenant:domain:"
	domainCacheTTL       = 5 * time.Minute

	entitlementCacheKeyPrefix = "tenant:entitlements:"
	entitlementCacheTTL       = 5 * time.Minute
)

// TenantContext is the multitenancy-internals.md §17 "Tenant context
// object" fields resolution alone can populate. Config caching (lazy,
// per-request) and the convenience methods (SchemaName, CacheKeyPrefix,
// etc.) belong to whatever downstream package actually consumes a
// resolved TenantContext, not to resolution itself.
type TenantContext struct {
	TenantID string
	Slug     string
	Name     string
	Plan     tenant.Plan
	Region   string
	Status   tenant.Status

	Entitlements EntitlementSet
}

// EntitlementSet is a typed map from feature key to value, per
// multitenancy-internals.md §4 — the merged result of a tenant's plan
// entitlements plus any active per-tenant overrides, built by
// LoadEntitlements.
type EntitlementSet struct {
	Features  map[string]bool  // "module.sales" -> true/false
	Limits    map[string]int64 // "users.max" -> 10, "storage_gb" -> 50
	Unlimited map[string]bool  // "users.max" -> true (overrides Limits)
}

func (e EntitlementSet) ModuleEnabled(name string) bool {
	return e.Features["module."+name]
}

func (e EntitlementSet) Limit(key string) (int64, bool) {
	if e.Unlimited[key] {
		return math.MaxInt64, true
	}
	if v, ok := e.Limits[key]; ok {
		return v, true
	}
	return 0, false
}

type Resolver struct {
	tenants *tenant.Store
	cache   *cache.Client
	billing *billing.Store
}

func NewResolver(tenants *tenant.Store, cacheClient *cache.Client, billingStore *billing.Store) *Resolver {
	return &Resolver{tenants: tenants, cache: cacheClient, billing: billingStore}
}

// ResolveByHost resolves host (an http.Request.Host value) to its tenant.
// Returns ErrTenantNotFound for an unresolved or deleted tenant, and
// ErrTenantSuspended for a suspended one — the caller maps these to 404
// and 403 respectively, never 401.
func (r *Resolver) ResolveByHost(ctx context.Context, host string) (*TenantContext, error) {
	t, err := r.tenantByDomain(ctx, normaliseDomain(host))
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrTenantNotFound
	}
	if t.Status == tenant.StatusSuspended {
		return nil, ErrTenantSuspended
	}

	ents, err := r.LoadEntitlements(ctx, t.ID)
	if err != nil {
		return nil, fmt.Errorf("load entitlements: %w", err)
	}

	return &TenantContext{
		TenantID:     t.ID,
		Slug:         t.Slug,
		Name:         t.Name,
		Plan:         t.Plan,
		Region:       t.Region,
		Status:       t.Status,
		Entitlements: ents,
	}, nil
}

// LoadEntitlements resolves tenantID's current entitlements — its plan's
// entitlements plus any active per-tenant overrides (overrides winning
// for a feature key both define), with any module the tenant has
// explicitly disabled (multitenancy-internals.md §8 "Explicit per-tenant
// module disable") forced off last, overriding even an override — cached
// in Redis for entitlementCacheTTL. multitenancy-internals.md §4's
// loadEntitlements. Any Redis error or decode failure on the cache read
// falls through to a live query, same fail-open convention tenantByDomain
// uses; a billing.Store query failure is a real error, not something to
// paper over with an empty EntitlementSet.
func (r *Resolver) LoadEntitlements(ctx context.Context, tenantID string) (EntitlementSet, error) {
	cacheKey := entitlementCacheKeyPrefix + tenantID
	if cached, found, err := r.cache.Get(ctx, cacheKey); err == nil && found {
		var ents EntitlementSet
		if err := msgpack.Unmarshal([]byte(cached), &ents); err == nil {
			return ents, nil
		}
	}

	planEnts, err := r.billing.PlanEntitlementsForTenant(ctx, tenantID)
	if err != nil {
		return EntitlementSet{}, fmt.Errorf("load plan entitlements: %w", err)
	}
	overrides, err := r.billing.ActiveOverridesForTenant(ctx, tenantID)
	if err != nil {
		return EntitlementSet{}, fmt.Errorf("load entitlement overrides: %w", err)
	}
	disabledModules, err := r.billing.DisabledModulesForTenant(ctx, tenantID)
	if err != nil {
		return EntitlementSet{}, fmt.Errorf("load disabled modules: %w", err)
	}

	ents := buildEntitlementSet(planEnts, overrides)
	for _, name := range disabledModules {
		ents.Features["module."+name] = false
	}
	r.setEntitlementCache(ctx, cacheKey, ents)
	return ents, nil
}

func (r *Resolver) setEntitlementCache(ctx context.Context, cacheKey string, ents EntitlementSet) {
	encoded, err := msgpack.Marshal(ents)
	if err != nil {
		return
	}
	_ = r.cache.SetWithTTL(ctx, cacheKey, string(encoded), entitlementCacheTTL)
}

// buildEntitlementSet merges planEnts and overrides into one EntitlementSet,
// applying overrides last so an override always wins over its plan's own
// entitlement for the same feature key.
func buildEntitlementSet(planEnts []billing.PlanEntitlement, overrides []billing.EntitlementOverride) EntitlementSet {
	ents := EntitlementSet{
		Features:  map[string]bool{},
		Limits:    map[string]int64{},
		Unlimited: map[string]bool{},
	}
	for _, pe := range planEnts {
		mergeEntitlement(&ents, pe.Feature, pe.Value)
	}
	for _, o := range overrides {
		mergeEntitlement(&ents, o.Feature, o.Value)
	}
	return ents
}

// mergeEntitlement classifies one feature/value row per
// multitenancy-internals.md §4's key convention: a "module.{name}" key is
// boolean module access; anything else is a numeric limit, or unlimited
// when value is the literal "unlimited". A non-module value that isn't
// "unlimited" and doesn't parse as an integer is logged and skipped —
// one bad row shouldn't fail entitlement loading for every other feature,
// same log-and-skip convention permcache.RolePermissionMap's own
// resolveRoleBitfield uses for an unknown permission name.
func mergeEntitlement(ents *EntitlementSet, feature, value string) {
	if strings.HasPrefix(feature, "module.") {
		ents.Features[feature] = value == "true"
		return
	}
	if value == "unlimited" {
		ents.Unlimited[feature] = true
		return
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		log.Warn().Str("feature", feature).Str("value", value).Msg("tenantresolve: unparseable entitlement value, skipping")
		return
	}
	ents.Limits[feature] = n
}

// cacheEntry is what's actually stored under a domain cache key — Found
// distinguishes a cached miss (domain doesn't resolve to any tenant) from
// a cached hit, so a repeated request for a nonexistent domain doesn't
// re-query Postgres within the TTL either.
type cacheEntry struct {
	Found  bool
	Tenant tenant.Tenant
}

// DomainCacheKey returns the Redis key ResolveByHost caches domain's
// resolution under — exported so cache-invalidating callers derive the
// same key rather than duplicating the format.
func DomainCacheKey(domain string) string {
	return domainCacheKeyPrefix + normaliseDomain(domain)
}

// EntitlementCacheKey returns the Redis key LoadEntitlements caches
// tenantID's entitlements under — exported so a caller that changes a
// tenant's plan (billing.Store.ChangeTenantPlan) can invalidate the same
// key rather than duplicating the format, same convention DomainCacheKey
// already follows.
func EntitlementCacheKey(tenantID string) string {
	return entitlementCacheKeyPrefix + tenantID
}

// tenantByDomain checks the Redis cache before falling back to Postgres.
// Any Redis error (read or write) fails open to a direct database lookup
// rather than blocking resolution on cache availability.
func (r *Resolver) tenantByDomain(ctx context.Context, domain string) (*tenant.Tenant, error) {
	cacheKey := domainCacheKeyPrefix + domain

	if cached, found, err := r.cache.Get(ctx, cacheKey); err == nil && found {
		var entry cacheEntry
		if err := msgpack.Unmarshal([]byte(cached), &entry); err == nil {
			if !entry.Found {
				return nil, nil
			}
			t := entry.Tenant
			return &t, nil
		}
	}

	t, err := r.tenants.GetByDomain(ctx, domain)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			r.setCache(ctx, cacheKey, nil)
			return nil, nil
		}
		return nil, fmt.Errorf("lookup tenant by domain: %w", err)
	}

	r.setCache(ctx, cacheKey, t)
	return t, nil
}

func (r *Resolver) setCache(ctx context.Context, cacheKey string, t *tenant.Tenant) {
	entry := cacheEntry{Found: t != nil}
	if t != nil {
		entry.Tenant = *t
	}

	encoded, err := msgpack.Marshal(entry)
	if err != nil {
		return
	}
	_ = r.cache.SetWithTTL(ctx, cacheKey, string(encoded), domainCacheTTL)
}

// normaliseDomain strips a port (if present) and lowercases host, per
// multitenancy-internals.md §4's domain lookup cache.
func normaliseDomain(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}
