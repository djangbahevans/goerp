// Package tenantresolve implements Class A tenant resolution —
// multitenancy-internals.md §4's "Resolution by route class" — for every
// ordinary module route and every already-authenticated /auth/* route.
// Class A resolves from the Host header alone, cached in Redis; the
// X-Tenant-ID/X-Tenant-Slug header and ?tenant= query-param sources
// belong to Class B/C's own anonymous, pre-session route handlers
// (auth-internals.md §9), not this package.
//
// Resolver doesn't wire into any actual HTTP middleware chain (goerp#91)
// and doesn't load entitlements (goerp#89's blocked sibling sub-issue,
// needs an unbuilt billing/plans/subscriptions schema) — TenantContext.
// Entitlements stays its zero value here.
package tenantresolve

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
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
// multitenancy-internals.md §4. Loading it (goerp#89's blocked sibling
// sub-issue) needs plan_entitlements/tenant_entitlement_overrides, neither
// of which exist yet — this type exists so TenantContext has somewhere to
// hold a zero value until that lands.
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
}

func NewResolver(tenants *tenant.Store, cacheClient *cache.Client) *Resolver {
	return &Resolver{tenants: tenants, cache: cacheClient}
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

	return &TenantContext{
		TenantID: t.ID,
		Slug:     t.Slug,
		Name:     t.Name,
		Plan:     t.Plan,
		Region:   t.Region,
		Status:   t.Status,
	}, nil
}

// cacheEntry is what's actually stored under a domain cache key — Found
// distinguishes a cached miss (domain doesn't resolve to any tenant) from
// a cached hit, so a repeated request for a nonexistent domain doesn't
// re-query Postgres within the TTL either.
type cacheEntry struct {
	Found  bool
	Tenant tenant.Tenant
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
