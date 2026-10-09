package providerselect

import (
	"errors"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/billing"
)

func TestReconcilePreservesDisableAndReplacesCategories(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	const name = "connector_multi"

	if err := f.store.Reconcile(ctx, f.tenantID, name, []string{CategorySMS, CategoryPush}); err != nil {
		t.Fatal(err)
	}

	for _, category := range []string{CategorySMS, CategoryPush} {
		if got, err := f.store.Resolve(ctx, f.tenantID, category); err != nil || got != name {
			t.Fatalf("resolve %s: %q, %v", category, got, err)
		}
		if err := f.store.SetPrimary(ctx, f.tenantID, name, category, ""); err != nil {
			t.Fatal(err)
		}
	}

	if got := f.selectionCount(t); got != 2 {
		t.Fatalf("selection count = %d, want independent selections for both categories", got)
	}

	if err := billing.NewStore(f.conn).SetModuleEnabledForTenant(ctx, f.tenantID, name, false, nil); err != nil {
		t.Fatal(err)
	}

	var disabledAt string
	if err := f.conn.QueryRowContext(ctx, `SELECT disabled_at::text FROM system.tenant_module_settings WHERE tenant_id = $1 AND module_name = $2`, f.tenantID, name).Scan(&disabledAt); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := f.store.Reconcile(ctx, f.tenantID, name, []string{CategoryPush, CategoryPayment}); err != nil {
			t.Fatal(err)
		}
	}

	var enabled bool
	var after string
	if err := f.conn.QueryRowContext(ctx, `SELECT enabled, disabled_at::text FROM system.tenant_module_settings WHERE tenant_id = $1 AND module_name = $2`, f.tenantID, name).Scan(&enabled, &after); err != nil {
		t.Fatal(err)
	}
	if enabled || after != disabledAt {
		t.Fatalf("disable choice changed: enabled=%v, disabled_at=%q, want %q", enabled, after, disabledAt)
	}

	if _, err := f.store.Resolve(ctx, f.tenantID, CategoryPush); !errors.Is(err, ErrNoProviderInstalled) {
		t.Fatalf("disabled push resolution = %v", err)
	}

	f.setEnabled(t, name, true)
	if _, err := f.store.Resolve(ctx, f.tenantID, CategorySMS); !errors.Is(err, ErrNoProviderInstalled) {
		t.Fatalf("removed SMS category resolution = %v", err)
	}
	if got, err := f.store.EnabledProviders(ctx, f.tenantID, CategoryPayment); err != nil || !slices.Equal(got, []string{name}) {
		t.Fatalf("payment providers = %v, %v", got, err)
	}

	if err := f.store.Reconcile(ctx, f.tenantID, name, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.store.IsEnabledProvider(ctx, f.tenantID, name, CategoryPush); err != nil || ok {
		t.Fatalf("category-free connector eligible for push: %v, %v", ok, err)
	}
}

func TestReconcileNonConnectorNeedsNoSetting(t *testing.T) {
	f := newFixture(t)

	if err := f.store.Reconcile(t.Context(), f.tenantID, "domain_plain", nil); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM system.tenant_module_settings WHERE tenant_id = $1`, f.tenantID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("settings count = %d, want 0", count)
	}
}

func TestReconcileConnectorWithoutProvidersCannotBecomePrimary(t *testing.T) {
	f := newFixture(t)

	if err := f.store.Reconcile(t.Context(), f.tenantID, "connector_plain", Categories(nil)); err != nil {
		t.Fatal(err)
	}

	if err := f.store.SetPrimary(t.Context(), f.tenantID, "connector_plain", CategorySMS, ""); !errors.Is(err, ErrModuleNotProvider) {
		t.Fatalf("set primary for category-free connector: %v", err)
	}
}
