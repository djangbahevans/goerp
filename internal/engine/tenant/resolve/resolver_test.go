package tenantresolve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// localPostgresDSN/localRedisConfig point directly at the compose.dev.yml
// instances, same convention as internal/engine/tenant's and
// internal/engine/cache's own tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func localRedisConfig() cache.Config {
	return cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1}
}

func openTestResolver(t *testing.T) (*Resolver, *tenant.Store, *sql.DB, *cache.Client) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cacheClient, err := cache.New(ctx, localRedisConfig())
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	return NewResolver(tenantStore, cacheClient), tenantStore, conn, cacheClient
}

func uniqueSlug(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("tr%d", time.Now().UnixNano())
}

func createTenant(t *testing.T, store *tenant.Store, conn *sql.DB, slug, name string) *tenant.Tenant {
	t.Helper()
	tt, err := store.CreateTenant(context.Background(), slug, name)
	if err != nil {
		t.Fatalf("CreateTenant(%q, %q) error: %v", slug, name, err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.tenants WHERE id = $1", tt.ID)
	})
	return tt
}

func insertDomain(t *testing.T, conn *sql.DB, tenantID, domain string) {
	t.Helper()
	_, err := conn.ExecContext(context.Background(), `
		INSERT INTO system.tenant_domains (tenant_id, domain, type)
		VALUES ($1, $2, 'subdomain')
	`, tenantID, domain)
	if err != nil {
		t.Fatalf("insert domain %q for tenant %q: %v", domain, tenantID, err)
	}
}

func TestResolveByHost_ResolvesActiveTenant(t *testing.T) {
	resolver, store, conn, _ := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Active Resolve")
	domain := created.Slug + ".example.com"
	insertDomain(t, conn, created.ID, domain)

	got, err := resolver.ResolveByHost(context.Background(), domain)
	if err != nil {
		t.Fatalf("ResolveByHost() error: %v", err)
	}
	if got.TenantID != created.ID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, created.ID)
	}
	if got.Slug != created.Slug {
		t.Errorf("Slug = %q, want %q", got.Slug, created.Slug)
	}
}

func TestResolveByHost_StripsPortAndLowercases(t *testing.T) {
	resolver, store, conn, _ := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Mixed Case")
	domain := created.Slug + ".example.com"
	insertDomain(t, conn, created.ID, domain)

	got, err := resolver.ResolveByHost(context.Background(), strings.ToUpper(domain)+":8443")
	if err != nil {
		t.Fatalf("ResolveByHost() with uppercase host + port error: %v", err)
	}
	if got.TenantID != created.ID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, created.ID)
	}
}

func TestResolveByHost_UnresolvedDomainReturnsErrTenantNotFound(t *testing.T) {
	resolver, _, _, _ := openTestResolver(t)

	_, err := resolver.ResolveByHost(context.Background(), "nonexistent-"+uniqueSlug(t)+".example.com")
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("ResolveByHost() error = %v, want ErrTenantNotFound", err)
	}
}

func TestResolveByHost_SuspendedTenantReturnsErrTenantSuspended(t *testing.T) {
	resolver, store, conn, _ := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Suspended Resolve")
	domain := created.Slug + ".example.com"
	insertDomain(t, conn, created.ID, domain)

	if _, err := store.UpdateStatus(context.Background(), created.Slug, tenant.StatusSuspended, nil); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}

	_, err := resolver.ResolveByHost(context.Background(), domain)
	if !errors.Is(err, ErrTenantSuspended) {
		t.Errorf("ResolveByHost() error = %v, want ErrTenantSuspended", err)
	}
}

func TestResolveByHost_DeletedTenantReturnsErrTenantNotFound(t *testing.T) {
	resolver, store, conn, _ := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Deleted Resolve")
	domain := created.Slug + ".example.com"
	insertDomain(t, conn, created.ID, domain)

	if _, err := store.UpdateStatus(context.Background(), created.Slug, tenant.StatusDeleted, nil); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}

	_, err := resolver.ResolveByHost(context.Background(), domain)
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("ResolveByHost() error = %v, want ErrTenantNotFound (never a different response for a deleted tenant)", err)
	}
}

func TestResolveByHost_CachesPositiveResultAcrossStoreDeletion(t *testing.T) {
	resolver, store, conn, cacheClient := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Cached Positive")
	domain := created.Slug + ".example.com"
	insertDomain(t, conn, created.ID, domain)

	if _, err := resolver.ResolveByHost(context.Background(), domain); err != nil {
		t.Fatalf("first ResolveByHost() error: %v", err)
	}

	// Delete the underlying row directly (bypassing the cache-invalidating
	// path this ticket doesn't implement) — a second resolve within the
	// TTL should still succeed from cache.
	if _, err := conn.ExecContext(context.Background(), "DELETE FROM system.tenants WHERE id = $1", created.ID); err != nil {
		t.Fatalf("delete tenant row: %v", err)
	}

	got, err := resolver.ResolveByHost(context.Background(), domain)
	if err != nil {
		t.Fatalf("second ResolveByHost() error: %v, want a cached hit despite the row being gone", err)
	}
	if got.TenantID != created.ID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, created.ID)
	}

	value, found, err := cacheClient.Get(context.Background(), domainCacheKeyPrefix+domain)
	if err != nil {
		t.Fatalf("cacheClient.Get() error: %v", err)
	}
	if !found || value == "" {
		t.Error("expected the domain cache key to be populated after ResolveByHost()")
	}
}

func TestResolveByHost_CachesNegativeResult(t *testing.T) {
	resolver, store, conn, _ := openTestResolver(t)
	domain := uniqueSlug(t) + ".example.com"

	_, err := resolver.ResolveByHost(context.Background(), domain)
	if !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("first ResolveByHost() error = %v, want ErrTenantNotFound", err)
	}

	// Now create a tenant with that exact domain — if the negative cache
	// weren't in effect, this would resolve; the TTL means it stays a
	// miss until it expires.
	created := createTenant(t, store, conn, uniqueSlug(t), "Should Stay Cached Miss")
	insertDomain(t, conn, created.ID, domain)

	_, err = resolver.ResolveByHost(context.Background(), domain)
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("second ResolveByHost() error = %v, want cached ErrTenantNotFound to still apply", err)
	}
}

func TestDomainCacheKey_MatchesResolveByHostsCacheKey(t *testing.T) {
	resolver, store, conn, cacheClient := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Cache Key Match")
	domain := "MixedCase." + created.Slug + ".example.com:8443"
	insertDomain(t, conn, created.ID, strings.ToLower(strings.TrimSuffix(domain, ":8443")))

	if _, err := resolver.ResolveByHost(context.Background(), domain); err != nil {
		t.Fatalf("ResolveByHost() error: %v", err)
	}

	_, found, err := cacheClient.Get(context.Background(), DomainCacheKey(domain))
	if err != nil {
		t.Fatalf("cacheClient.Get() error: %v", err)
	}
	if !found {
		t.Error("DomainCacheKey(domain) did not match the key ResolveByHost cached under")
	}
}

func TestEntitlementSet_LimitAndModuleEnabled(t *testing.T) {
	set := EntitlementSet{
		Features:  map[string]bool{"module.sales": true},
		Limits:    map[string]int64{"users.max": 10},
		Unlimited: map[string]bool{"storage_gb": true},
	}

	if !set.ModuleEnabled("sales") {
		t.Error("ModuleEnabled(\"sales\") = false, want true")
	}
	if set.ModuleEnabled("purchasing") {
		t.Error("ModuleEnabled(\"purchasing\") = true, want false")
	}

	if v, ok := set.Limit("users.max"); !ok || v != 10 {
		t.Errorf("Limit(\"users.max\") = (%d, %v), want (10, true)", v, ok)
	}
	if v, ok := set.Limit("storage_gb"); !ok || v == 0 {
		t.Errorf("Limit(\"storage_gb\") = (%d, %v), want (math.MaxInt64, true)", v, ok)
	}
	if _, ok := set.Limit("unknown"); ok {
		t.Error("Limit(\"unknown\") ok = true, want false")
	}
}
