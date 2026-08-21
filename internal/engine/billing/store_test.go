package billing

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// testEnv wires a billing.Store plus a tenant.Store against the real
// compose.dev.yml Postgres — tenant_subscriptions/tenant_entitlement_overrides
// both FK-reference system.tenants, so tests that exercise those tables
// need a real tenant row.
type testEnv struct {
	store       *Store
	tenantStore *tenant.Store
	conn        *sql.DB
}

func openTestStore(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}

	store := NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return &testEnv{store: store, tenantStore: tenantStore, conn: conn}
}

func uniqueName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("billingtest%d", time.Now().UnixNano())
}

// createPlan creates a plan, fails the test on error, and registers its
// scoped cleanup — the pattern every test below that needs a real plan
// row follows. plan_entitlements cascades on delete, so no separate
// cleanup is needed for entitlement rows.
func (e *testEnv) createPlan(t *testing.T, priceMonthly, priceYearly *int64) *Plan {
	t.Helper()
	name := uniqueName(t)
	p, err := e.store.CreatePlan(context.Background(), name, "Test Plan", priceMonthly, priceYearly)
	if err != nil {
		t.Fatalf("CreatePlan(%q) error: %v", name, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.plans WHERE id = $1", p.ID) })
	return p
}

// createTenant creates a tenant, fails the test on error, and registers
// its scoped cleanup — tenant_subscriptions/tenant_entitlement_overrides
// both cascade on delete, so no separate cleanup is needed for those rows.
func (e *testEnv) createTenant(t *testing.T) *tenant.Tenant {
	t.Helper()
	slug := uniqueName(t)
	tt, err := e.tenantStore.CreateTenant(context.Background(), slug, "Billing Test Co")
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	return tt
}

func TestBootstrap_CreatesAllFourTables(t *testing.T) {
	env := openTestStore(t)

	for _, table := range []string{"plans", "plan_entitlements", "tenant_subscriptions", "tenant_entitlement_overrides"} {
		var exists bool
		err := env.conn.QueryRowContext(context.Background(), `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'system' AND table_name = $1
			)
		`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %q exists: %v", table, err)
		}
		if !exists {
			t.Errorf("expected system.%s to exist after Bootstrap()", table)
		}
	}
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	env := openTestStore(t)

	if err := env.store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

// TestBootstrap_ConcurrentCallsAllSucceed guards against goerp#171 — see
// tenant.TestBootstrap_ConcurrentCallsAllSucceed's doc comment for what
// this does and doesn't prove.
func TestBootstrap_ConcurrentCallsAllSucceed(t *testing.T) {
	env := openTestStore(t)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- env.store.Bootstrap(context.Background())
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Bootstrap() error: %v", err)
		}
	}
}

func TestCreatePlan_Succeeds(t *testing.T) {
	env := openTestStore(t)
	monthly, yearly := int64(2900), int64(29000)

	p := env.createPlan(t, &monthly, &yearly)

	if p.ID == "" {
		t.Error("expected a generated ID")
	}
	if p.DisplayName != "Test Plan" {
		t.Errorf("DisplayName = %q, want %q", p.DisplayName, "Test Plan")
	}
	if p.PriceMonthly == nil || *p.PriceMonthly != monthly {
		t.Errorf("PriceMonthly = %v, want %d", p.PriceMonthly, monthly)
	}
	if p.PriceYearly == nil || *p.PriceYearly != yearly {
		t.Errorf("PriceYearly = %v, want %d", p.PriceYearly, yearly)
	}
	if p.Currency != "USD" {
		t.Errorf("Currency = %q, want default %q", p.Currency, "USD")
	}
	if !p.IsActive {
		t.Error("IsActive = false, want default true")
	}
	if p.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestCreatePlan_NilPricesAllowedForCustomPricing(t *testing.T) {
	env := openTestStore(t)

	p := env.createPlan(t, nil, nil)

	if p.PriceMonthly != nil {
		t.Errorf("PriceMonthly = %v, want nil", p.PriceMonthly)
	}
	if p.PriceYearly != nil {
		t.Errorf("PriceYearly = %v, want nil", p.PriceYearly)
	}
}

func TestCreatePlan_DuplicateNameFails(t *testing.T) {
	env := openTestStore(t)
	name := uniqueName(t)

	if _, err := env.store.CreatePlan(context.Background(), name, "First", nil, nil); err != nil {
		t.Fatalf("CreatePlan() error: %v", err)
	}
	t.Cleanup(func() { _, _ = env.conn.Exec("DELETE FROM system.plans WHERE name = $1", name) })

	if _, err := env.store.CreatePlan(context.Background(), name, "Second", nil, nil); err == nil {
		t.Fatal("expected an error creating a plan with a duplicate name")
	}
}

func TestUpsertPlanEntitlement_InsertsThenUpdatesOnConflict(t *testing.T) {
	env := openTestStore(t)
	ctx := context.Background()
	p := env.createPlan(t, nil, nil)

	if err := env.store.UpsertPlanEntitlement(ctx, p.ID, "users.max", "10"); err != nil {
		t.Fatalf("first UpsertPlanEntitlement() error: %v", err)
	}
	if err := env.store.UpsertPlanEntitlement(ctx, p.ID, "users.max", "50"); err != nil {
		t.Fatalf("second UpsertPlanEntitlement() error: %v", err)
	}

	var value string
	if err := env.conn.QueryRowContext(ctx,
		"SELECT value FROM system.plan_entitlements WHERE plan_id = $1 AND feature = $2", p.ID, "users.max",
	).Scan(&value); err != nil {
		t.Fatalf("query entitlement: %v", err)
	}
	if value != "50" {
		t.Errorf("value = %q, want %q (the conflict update, not a duplicate row)", value, "50")
	}
}

func TestCreateSubscription_DefaultsToTrialing(t *testing.T) {
	env := openTestStore(t)
	ctx := context.Background()
	p := env.createPlan(t, nil, nil)
	tt := env.createTenant(t)
	start := time.Now()
	end := start.Add(30 * 24 * time.Hour)

	sub, err := env.store.CreateSubscription(ctx, tt.ID, p.ID, start, end)
	if err != nil {
		t.Fatalf("CreateSubscription() error: %v", err)
	}
	if sub.Status != SubscriptionTrialing {
		t.Errorf("Status = %q, want default %q", sub.Status, SubscriptionTrialing)
	}
	if sub.TenantID != tt.ID {
		t.Errorf("TenantID = %q, want %q", sub.TenantID, tt.ID)
	}
	if sub.PlanID != p.ID {
		t.Errorf("PlanID = %q, want %q", sub.PlanID, p.ID)
	}
}

func TestCreateSubscription_UnknownPlanFails(t *testing.T) {
	env := openTestStore(t)
	tt := env.createTenant(t)
	now := time.Now()

	_, err := env.store.CreateSubscription(context.Background(), tt.ID, "00000000-0000-0000-0000-000000000000", now, now.Add(time.Hour))
	if err == nil {
		t.Fatal("expected a foreign key violation for an unknown plan")
	}
}

func TestCreateSubscription_UnknownTenantFails(t *testing.T) {
	env := openTestStore(t)
	p := env.createPlan(t, nil, nil)
	now := time.Now()

	_, err := env.store.CreateSubscription(context.Background(), "00000000-0000-0000-0000-000000000000", p.ID, now, now.Add(time.Hour))
	if err == nil {
		t.Fatal("expected a foreign key violation for an unknown tenant")
	}
}

func TestUpsertEntitlementOverride_InsertsThenUpdatesOnConflict(t *testing.T) {
	env := openTestStore(t)
	ctx := context.Background()
	tt := env.createTenant(t)

	if err := env.store.UpsertEntitlementOverride(ctx, tt.ID, "users.max", "100", nil, nil, nil); err != nil {
		t.Fatalf("first UpsertEntitlementOverride() error: %v", err)
	}
	reason := "enterprise deal"
	if err := env.store.UpsertEntitlementOverride(ctx, tt.ID, "users.max", "unlimited", &reason, nil, nil); err != nil {
		t.Fatalf("second UpsertEntitlementOverride() error: %v", err)
	}

	overrides, err := env.store.ActiveOverridesForTenant(ctx, tt.ID)
	if err != nil {
		t.Fatalf("ActiveOverridesForTenant() error: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("len(overrides) = %d, want 1 (conflict update, not a duplicate row)", len(overrides))
	}
	if overrides[0].Value != "unlimited" {
		t.Errorf("Value = %q, want %q", overrides[0].Value, "unlimited")
	}
	if overrides[0].Reason == nil || *overrides[0].Reason != reason {
		t.Errorf("Reason = %v, want %q", overrides[0].Reason, reason)
	}
}

func TestActiveOverridesForTenant_ExcludesExpired(t *testing.T) {
	env := openTestStore(t)
	ctx := context.Background()
	tt := env.createTenant(t)

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	if err := env.store.UpsertEntitlementOverride(ctx, tt.ID, "expired.feature", "true", nil, &past, nil); err != nil {
		t.Fatalf("upsert expired override: %v", err)
	}
	if err := env.store.UpsertEntitlementOverride(ctx, tt.ID, "active.feature", "true", nil, &future, nil); err != nil {
		t.Fatalf("upsert active override: %v", err)
	}

	overrides, err := env.store.ActiveOverridesForTenant(ctx, tt.ID)
	if err != nil {
		t.Fatalf("ActiveOverridesForTenant() error: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("len(overrides) = %d, want 1", len(overrides))
	}
	if overrides[0].Feature != "active.feature" {
		t.Errorf("Feature = %q, want %q", overrides[0].Feature, "active.feature")
	}
}

func TestPlanEntitlementsForTenant_OnlyReturnsActiveOrTrialingSubscriptions(t *testing.T) {
	env := openTestStore(t)
	ctx := context.Background()

	trialingPlan := env.createPlan(t, nil, nil)
	if err := env.store.UpsertPlanEntitlement(ctx, trialingPlan.ID, "module.sales", "true"); err != nil {
		t.Fatalf("upsert trialing plan entitlement: %v", err)
	}
	cancelledPlan := env.createPlan(t, nil, nil)
	if err := env.store.UpsertPlanEntitlement(ctx, cancelledPlan.ID, "module.hr", "true"); err != nil {
		t.Fatalf("upsert cancelled plan entitlement: %v", err)
	}

	tt := env.createTenant(t)
	now := time.Now()
	if _, err := env.store.CreateSubscription(ctx, tt.ID, trialingPlan.ID, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("create trialing subscription: %v", err)
	}
	cancelledSub, err := env.store.CreateSubscription(ctx, tt.ID, cancelledPlan.ID, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("create cancelled subscription: %v", err)
	}
	if _, err := env.conn.ExecContext(ctx, "UPDATE system.tenant_subscriptions SET status = 'cancelled' WHERE id = $1", cancelledSub.ID); err != nil {
		t.Fatalf("mark subscription cancelled: %v", err)
	}

	ents, err := env.store.PlanEntitlementsForTenant(ctx, tt.ID)
	if err != nil {
		t.Fatalf("PlanEntitlementsForTenant() error: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("len(ents) = %d, want 1", len(ents))
	}
	if ents[0].Feature != "module.sales" {
		t.Errorf("Feature = %q, want %q (from the trialing subscription's plan, not the cancelled one)", ents[0].Feature, "module.sales")
	}
}
