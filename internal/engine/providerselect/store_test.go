package providerselect

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

type fixture struct {
	store    *Store
	conn     *sql.DB
	tenantID string
}

// newFixture wires a Store against the real compose.dev.yml Postgres with
// a fresh tenant; tenant_module_settings (billing.Store) and system.users
// must exist before this package's own table.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := t.Context()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	if err := billing.NewStore(conn).Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}
	if err := user.NewStore(conn).Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}
	store := NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("providerselecttest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Provider Select Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })

	return &fixture{store: store, conn: conn, tenantID: tt.ID}
}

// install writes the tenant_module_settings row module install would:
// enabled, with provider_category from the manifest's provides.
func (f *fixture) install(t *testing.T, moduleName, category string, enabled bool) {
	t.Helper()
	var cat any
	if category != "" {
		cat = category
	}
	if _, err := f.conn.Exec(`
		INSERT INTO system.tenant_module_settings (tenant_id, module_name, enabled, provider_category)
		VALUES ($1, $2, $3, $4)
	`, f.tenantID, moduleName, enabled, cat); err != nil {
		t.Fatalf("install %s: %v", moduleName, err)
	}
}

func (f *fixture) setEnabled(t *testing.T, moduleName string, enabled bool) {
	t.Helper()
	if _, err := f.conn.Exec(`
		UPDATE system.tenant_module_settings SET enabled = $3 WHERE tenant_id = $1 AND module_name = $2
	`, f.tenantID, moduleName, enabled); err != nil {
		t.Fatalf("set %s enabled=%v: %v", moduleName, enabled, err)
	}
}

func (f *fixture) selectionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.conn.QueryRow(`SELECT count(*) FROM system.tenant_provider_selections WHERE tenant_id = $1`, f.tenantID).Scan(&n); err != nil {
		t.Fatalf("count selections: %v", err)
	}
	return n
}

func (f *fixture) resolve(t *testing.T, category string) (string, error) {
	t.Helper()
	return f.store.Resolve(t.Context(), f.tenantID, category)
}

func TestResolve_SoleProviderResolvesWithoutARow(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_twilio", CategorySMS, true)

	got, err := f.resolve(t, CategorySMS)
	if err != nil || got != "connector_twilio" {
		t.Fatalf("Resolve() = %q, %v; want connector_twilio", got, err)
	}
	if n := f.selectionCount(t); n != 0 {
		t.Fatalf("selection rows = %d, want 0", n)
	}
}

func TestResolve_AmbiguousWithoutSelection(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_twilio", CategorySMS, true)
	f.install(t, "connector_africastalking", CategorySMS, true)

	if _, err := f.resolve(t, CategorySMS); !errors.Is(err, ErrNoProviderSelected) {
		t.Fatalf("Resolve() error = %v, want ErrNoProviderSelected", err)
	}
}

func TestResolve_NoneInstalled(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_fcm", CategoryPush, true)
	f.install(t, "connector_twilio", CategorySMS, false)

	if _, err := f.resolve(t, CategorySMS); !errors.Is(err, ErrNoProviderInstalled) {
		t.Fatalf("Resolve() error = %v, want ErrNoProviderInstalled", err)
	}
}

func TestSetPrimary_ThenResolveReturnsSelection(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_twilio", CategorySMS, true)
	f.install(t, "connector_africastalking", CategorySMS, true)

	for _, module := range []string{"connector_africastalking", "connector_twilio"} {
		category, err := f.store.SetPrimary(t.Context(), f.tenantID, module, "")
		if err != nil || category != CategorySMS {
			t.Fatalf("SetPrimary(%s) = %q, %v; want %q", module, category, err, CategorySMS)
		}
		got, err := f.resolve(t, CategorySMS)
		if err != nil || got != module {
			t.Fatalf("Resolve() after SetPrimary(%s) = %q, %v", module, got, err)
		}
	}
	if n := f.selectionCount(t); n != 1 {
		t.Fatalf("selection rows = %d, want 1", n)
	}
}

func TestSetPrimary_RecordsSelectedBy(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_twilio", CategorySMS, true)

	userID, err := user.NewStore(f.conn).FindOrCreateInvited(t.Context(), fmt.Sprintf("providerselecttest%d@example.com", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })

	if _, err := f.store.SetPrimary(t.Context(), f.tenantID, "connector_twilio", userID); err != nil {
		t.Fatalf("SetPrimary() error: %v", err)
	}
	var selectedBy string
	if err := f.conn.QueryRow(`SELECT selected_by FROM system.tenant_provider_selections WHERE tenant_id = $1`, f.tenantID).Scan(&selectedBy); err != nil {
		t.Fatalf("load selected_by: %v", err)
	}
	if selectedBy != userID {
		t.Fatalf("selected_by = %q, want %q", selectedBy, userID)
	}
}

func TestResolve_DisabledSelectionFallsBack(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_twilio", CategorySMS, true)
	f.install(t, "connector_africastalking", CategorySMS, true)
	if _, err := f.store.SetPrimary(t.Context(), f.tenantID, "connector_africastalking", ""); err != nil {
		t.Fatalf("SetPrimary() error: %v", err)
	}

	f.setEnabled(t, "connector_africastalking", false)
	got, err := f.resolve(t, CategorySMS)
	if err != nil || got != "connector_twilio" {
		t.Fatalf("Resolve() = %q, %v; want the sole enabled connector_twilio", got, err)
	}

	f.install(t, "connector_hubtel", CategorySMS, true)
	if _, err := f.resolve(t, CategorySMS); !errors.Is(err, ErrNoProviderSelected) {
		t.Fatalf("Resolve() error = %v, want ErrNoProviderSelected", err)
	}
}

func TestPaymentProvider_NeverSelected(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_paystack", CategoryPayment, true)
	f.install(t, "connector_mtn_momo", CategoryPayment, true)

	if _, err := f.store.SetPrimary(t.Context(), f.tenantID, "connector_paystack", ""); !errors.Is(err, ErrNotSingleActive) {
		t.Fatalf("SetPrimary(payment) error = %v, want ErrNotSingleActive", err)
	}
	if _, err := f.resolve(t, CategoryPayment); !errors.Is(err, ErrNotSingleActive) {
		t.Fatalf("Resolve(payment) error = %v, want ErrNotSingleActive", err)
	}
	if _, err := f.conn.Exec(`
		INSERT INTO system.tenant_provider_selections (tenant_id, category, module_name)
		VALUES ($1, 'payment_provider', 'connector_paystack')
	`, f.tenantID); err == nil {
		t.Fatal("direct payment_provider insert succeeded, want CHECK violation")
	}
	if n := f.selectionCount(t); n != 0 {
		t.Fatalf("selection rows = %d, want 0", n)
	}

	providers, err := f.store.EnabledProviders(t.Context(), f.tenantID, CategoryPayment)
	if err != nil || len(providers) != 2 {
		t.Fatalf("EnabledProviders(payment) = %v, %v; want both", providers, err)
	}
}

func TestSetPrimary_RejectsIneligibleModules(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_twilio", CategorySMS, false)
	f.install(t, "crm", "", true)
	f.install(t, "connector_fax", "fax_provider", true)

	cases := []struct {
		module string
		want   error
	}{
		{"connector_missing", ErrModuleNotEnabled},
		{"connector_twilio", ErrModuleNotEnabled},
		{"crm", ErrModuleNotProvider},
		{"connector_fax", ErrModuleNotProvider},
	}
	for _, c := range cases {
		if _, err := f.store.SetPrimary(t.Context(), f.tenantID, c.module, ""); !errors.Is(err, c.want) {
			t.Errorf("SetPrimary(%s) error = %v, want %v", c.module, err, c.want)
		}
	}
	if n := f.selectionCount(t); n != 0 {
		t.Fatalf("selection rows = %d, want 0", n)
	}
}

func TestSetPrimary_ConcurrentSwitchesLeaveOneRow(t *testing.T) {
	f := newFixture(t)
	modules := []string{"connector_a", "connector_b", "connector_c", "connector_d"}
	for _, m := range modules {
		f.install(t, m, CategorySMS, true)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 5*len(modules))
	for range 5 {
		for _, m := range modules {
			wg.Go(func() {
				if _, err := f.store.SetPrimary(t.Context(), f.tenantID, m, ""); err != nil {
					errs <- err
				}
			})
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("SetPrimary() error: %v", err)
	}

	if n := f.selectionCount(t); n != 1 {
		t.Fatalf("selection rows = %d, want 1", n)
	}
	got, err := f.resolve(t, CategorySMS)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if !slices.Contains(modules, got) {
		t.Fatalf("Resolve() = %q, want one of %v", got, modules)
	}
}

func TestIsEnabledProvider(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_paystack", CategoryPayment, true)
	f.install(t, "connector_mtn_momo", CategoryPayment, false)
	f.install(t, "connector_twilio", CategorySMS, true)

	for _, tt := range []struct {
		module, category string
		want             bool
	}{
		{"connector_paystack", CategoryPayment, true},
		{"connector_mtn_momo", CategoryPayment, false},
		{"connector_twilio", CategoryPayment, false},
		{"connector_missing", CategoryPayment, false},
	} {
		got, err := f.store.IsEnabledProvider(t.Context(), f.tenantID, tt.module, tt.category)
		if err != nil || got != tt.want {
			t.Errorf("IsEnabledProvider(%s, %s) = %v, %v; want %v", tt.module, tt.category, got, err, tt.want)
		}
	}
}

func TestIsCategory(t *testing.T) {
	for _, c := range []string{CategorySMS, CategoryPush, CategoryOAuth, CategoryPayment} {
		if !IsCategory(c) {
			t.Errorf("IsCategory(%q) = false", c)
		}
	}
	if IsCategory("email_provider") || IsCategory("") {
		t.Error("IsCategory accepted a non-provider category")
	}
	if !IsMultiActive(CategoryPayment) || IsMultiActive(CategorySMS) {
		t.Error("IsMultiActive: only payment_provider is multi-active")
	}
}
