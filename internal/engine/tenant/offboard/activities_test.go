package tenantoffboard

import (
	"context"
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

// TestMarkOffboarding_InvalidatesWarmDomainCache guards goerp#631: a
// domain cached by a prior tenantresolve.Resolver.ResolveByHost call
// (the realistic case — an active tenant almost certainly has a warm
// cache entry by the time it's offboarded) must not keep returning the
// pre-offboarding status for domainCacheTTL after MarkOffboarding runs.
func TestMarkOffboarding_InvalidatesWarmDomainCache(t *testing.T) {
	env := newTestEnv(t, nil)
	slug := uniqueSlug(t)
	tt := env.activeTenant(t, slug)
	ctx := context.Background()

	domain := slug + ".example.com"
	if _, err := env.tenantStore.CreateDomain(ctx, tt.ID, domain, tenant.DomainSubdomain, true); err != nil {
		t.Fatalf("CreateDomain() error: %v", err)
	}

	billingStore := billing.NewStore(env.conn)
	if err := billingStore.Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}
	resolver := tenantresolve.NewResolver(env.tenantStore, env.cacheClient, billingStore)

	if _, err := resolver.ResolveByHost(ctx, domain); err != nil {
		t.Fatalf("prime ResolveByHost() error: %v", err)
	}
	if _, found, err := env.cacheClient.Get(ctx, tenantresolve.DomainCacheKey(domain)); err != nil || !found {
		t.Fatalf("expected domain cache to be primed before offboarding; found=%v err=%v", found, err)
	}

	if err := env.activities.MarkOffboarding(ctx, slug); err != nil {
		t.Fatalf("MarkOffboarding() error: %v", err)
	}

	if _, found, err := env.cacheClient.Get(ctx, tenantresolve.DomainCacheKey(domain)); err != nil || found {
		t.Errorf("expected domain cache to be invalidated after MarkOffboarding; found=%v err=%v", found, err)
	}

	if _, err := resolver.ResolveByHost(ctx, domain); !errors.Is(err, tenantresolve.ErrTenantOffboarding) {
		t.Errorf("ResolveByHost() error = %v, want ErrTenantOffboarding (not a stale cached status)", err)
	}
}
