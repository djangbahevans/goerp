package tenantresolve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/billing"
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

func openTestResolver(t *testing.T) (*Resolver, *tenant.Store, *sql.DB, *cache.Client, *billing.Store) {
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

	billingStore := billing.NewStore(conn)
	if err := billingStore.Bootstrap(context.Background()); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cacheClient, err := cache.New(ctx, localRedisConfig())
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	return NewResolver(tenantStore, cacheClient, billingStore), tenantStore, conn, cacheClient, billingStore
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
	resolver, store, conn, _, _ := openTestResolver(t)
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
	resolver, store, conn, _, _ := openTestResolver(t)
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
	resolver, _, _, _, _ := openTestResolver(t)

	_, err := resolver.ResolveByHost(context.Background(), "nonexistent-"+uniqueSlug(t)+".example.com")
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("ResolveByHost() error = %v, want ErrTenantNotFound", err)
	}
}

func TestResolveByHost_SuspendedTenantReturnsErrTenantSuspended(t *testing.T) {
	resolver, store, conn, _, _ := openTestResolver(t)
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
	resolver, store, conn, _, _ := openTestResolver(t)
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
	resolver, store, conn, cacheClient, _ := openTestResolver(t)
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
	resolver, store, conn, _, _ := openTestResolver(t)
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
	resolver, store, conn, cacheClient, _ := openTestResolver(t)
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

func TestEntitlementCacheKey_MatchesLoadEntitlementsCacheKey(t *testing.T) {
	resolver, store, conn, cacheClient, _ := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Entitlement Cache Key Match")

	if _, err := resolver.LoadEntitlements(context.Background(), created.ID); err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}

	_, found, err := cacheClient.Get(context.Background(), EntitlementCacheKey(created.ID))
	if err != nil {
		t.Fatalf("cacheClient.Get() error: %v", err)
	}
	if !found {
		t.Error("EntitlementCacheKey(tenantID) did not match the key LoadEntitlements cached under")
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

// createPlanWithEntitlement creates a plan granting exactly one
// feature/value entitlement, registering the plan row's cleanup (which
// cascades to plan_entitlements) — the pattern every LoadEntitlements test
// below follows.
func createPlanWithEntitlement(t *testing.T, billingStore *billing.Store, conn *sql.DB, feature, value string) *billing.Plan {
	t.Helper()
	name := fmt.Sprintf("trplan%d", time.Now().UnixNano())
	p, err := billingStore.CreatePlan(context.Background(), name, "Test Plan", nil, nil)
	if err != nil {
		t.Fatalf("CreatePlan() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DELETE FROM system.plans WHERE id = $1", p.ID) })

	if err := billingStore.UpsertPlanEntitlement(context.Background(), p.ID, feature, value); err != nil {
		t.Fatalf("UpsertPlanEntitlement() error: %v", err)
	}
	return p
}

// subscribeTenant subscribes tenantID to planID — tenant_subscriptions
// cascades on the tenant's own cleanup (createTenant), so no separate
// cleanup is needed here.
func subscribeTenant(t *testing.T, billingStore *billing.Store, tenantID, planID string) {
	t.Helper()
	now := time.Now()
	if _, err := billingStore.CreateSubscription(context.Background(), tenantID, planID, now, now.Add(30*24*time.Hour)); err != nil {
		t.Fatalf("CreateSubscription() error: %v", err)
	}
}

func TestLoadEntitlements_MergesModuleFeatureFromPlan(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Module Feature")
	plan := createPlanWithEntitlement(t, billingStore, conn, "module.sales", "true")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	ents, err := resolver.LoadEntitlements(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if !ents.ModuleEnabled("sales") {
		t.Error("ModuleEnabled(\"sales\") = false, want true")
	}
}

func TestLoadEntitlements_MergesNumericLimitFromPlan(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Numeric Limit")
	plan := createPlanWithEntitlement(t, billingStore, conn, "users.max", "10")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	ents, err := resolver.LoadEntitlements(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if v, ok := ents.Limit("users.max"); !ok || v != 10 {
		t.Errorf("Limit(\"users.max\") = (%d, %v), want (10, true)", v, ok)
	}
}

func TestLoadEntitlements_UnlimitedValueSetsUnlimitedFlag(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Unlimited")
	plan := createPlanWithEntitlement(t, billingStore, conn, "storage_gb", "unlimited")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	ents, err := resolver.LoadEntitlements(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if v, ok := ents.Limit("storage_gb"); !ok || v != math.MaxInt64 {
		t.Errorf("Limit(\"storage_gb\") = (%d, %v), want (math.MaxInt64, true)", v, ok)
	}
}

func TestLoadEntitlements_OverrideWinsOverPlanEntitlement(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Override Wins")
	plan := createPlanWithEntitlement(t, billingStore, conn, "users.max", "10")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	if err := billingStore.UpsertEntitlementOverride(context.Background(), created.ID, "users.max", "50", nil, nil, nil); err != nil {
		t.Fatalf("UpsertEntitlementOverride() error: %v", err)
	}

	ents, err := resolver.LoadEntitlements(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if v, ok := ents.Limit("users.max"); !ok || v != 50 {
		t.Errorf("Limit(\"users.max\") = (%d, %v), want (50, true) — override should win", v, ok)
	}
}

func TestLoadEntitlements_ExpiredOverrideIsIgnored(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Expired Override")
	plan := createPlanWithEntitlement(t, billingStore, conn, "users.max", "10")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	past := time.Now().Add(-time.Hour)
	if err := billingStore.UpsertEntitlementOverride(context.Background(), created.ID, "users.max", "999", nil, &past, nil); err != nil {
		t.Fatalf("UpsertEntitlementOverride() error: %v", err)
	}

	ents, err := resolver.LoadEntitlements(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if v, ok := ents.Limit("users.max"); !ok || v != 10 {
		t.Errorf("Limit(\"users.max\") = (%d, %v), want (10, true) — expired override must not apply", v, ok)
	}
}

func TestLoadEntitlements_ExplicitTenantDisableOverridesPlanEntitlement(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Module Disable")
	plan := createPlanWithEntitlement(t, billingStore, conn, "module.hr", "true")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	if err := billingStore.SetModuleEnabledForTenant(context.Background(), created.ID, "hr", false, nil); err != nil {
		t.Fatalf("SetModuleEnabledForTenant() error: %v", err)
	}

	ents, err := resolver.LoadEntitlements(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if ents.ModuleEnabled("hr") {
		t.Error("ModuleEnabled(\"hr\") = true, want false — explicit tenant disable should override plan entitlement")
	}
}

func TestLoadEntitlements_ExplicitTenantDisableOverridesEntitlementOverride(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Module Disable Beats Override")
	plan := createPlanWithEntitlement(t, billingStore, conn, "module.hr", "false")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	// An enterprise-deal override that grants the module...
	if err := billingStore.UpsertEntitlementOverride(context.Background(), created.ID, "module.hr", "true", nil, nil, nil); err != nil {
		t.Fatalf("UpsertEntitlementOverride() error: %v", err)
	}
	// ...still loses to the tenant's own explicit disable.
	if err := billingStore.SetModuleEnabledForTenant(context.Background(), created.ID, "hr", false, nil); err != nil {
		t.Fatalf("SetModuleEnabledForTenant() error: %v", err)
	}

	ents, err := resolver.LoadEntitlements(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if ents.ModuleEnabled("hr") {
		t.Error("ModuleEnabled(\"hr\") = true, want false — explicit tenant disable should beat even an entitlement override")
	}
}

func TestLoadEntitlements_NoTenantModuleSettingsRowLeavesPlanEntitlementUnaffected(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "No Disable Row")
	plan := createPlanWithEntitlement(t, billingStore, conn, "module.sales", "true")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	ents, err := resolver.LoadEntitlements(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if !ents.ModuleEnabled("sales") {
		t.Error("ModuleEnabled(\"sales\") = false, want true — no tenant_module_settings row means unaffected by explicit disable")
	}
}

func TestLoadEntitlements_CachesResult(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Cached Entitlements")
	plan := createPlanWithEntitlement(t, billingStore, conn, "users.max", "10")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	if _, err := resolver.LoadEntitlements(context.Background(), created.ID); err != nil {
		t.Fatalf("first LoadEntitlements() error: %v", err)
	}

	// Delete the underlying subscription — a second call within the TTL
	// should still return the cached (now-orphaned) result.
	if _, err := conn.ExecContext(context.Background(), "DELETE FROM system.tenant_subscriptions WHERE tenant_id = $1", created.ID); err != nil {
		t.Fatalf("delete subscription: %v", err)
	}

	ents, err := resolver.LoadEntitlements(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("second LoadEntitlements() error: %v", err)
	}
	if v, ok := ents.Limit("users.max"); !ok || v != 10 {
		t.Errorf("Limit(\"users.max\") = (%d, %v), want (10, true) from cache despite the row being gone", v, ok)
	}
}

func TestResolveByHost_PopulatesEntitlements(t *testing.T) {
	resolver, store, conn, _, billingStore := openTestResolver(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Resolve Entitlements")
	domain := created.Slug + ".example.com"
	insertDomain(t, conn, created.ID, domain)
	plan := createPlanWithEntitlement(t, billingStore, conn, "module.sales", "true")
	subscribeTenant(t, billingStore, created.ID, plan.ID)

	got, err := resolver.ResolveByHost(context.Background(), domain)
	if err != nil {
		t.Fatalf("ResolveByHost() error: %v", err)
	}
	if !got.Entitlements.ModuleEnabled("sales") {
		t.Error("Entitlements.ModuleEnabled(\"sales\") = false, want true")
	}
}
