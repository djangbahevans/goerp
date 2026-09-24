package password

import (
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func newPolicyEnv(t *testing.T) (*PolicyStore, *tenantconfig.Store, string) {
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
	config := tenantconfig.NewStore(conn)
	if err := config.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}

	tt, err := tenantStore.CreateTenant(ctx, fmt.Sprintf("pwpolicytest%d", time.Now().UnixNano()), "Password Policy Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })

	return NewPolicyStore(config), config, tt.ID
}

func TestEffective_UnconfiguredTenantGetsGlobalAtVersionZero(t *testing.T) {
	store, _, tenantID := newPolicyEnv(t)

	policy, version, err := store.Effective(t.Context(), tenantID)
	if err != nil {
		t.Fatalf("Effective() error: %v", err)
	}
	if policy != Global || version != 0 {
		t.Errorf("Effective() = %+v, %d, want Global, 0", policy, version)
	}
}

func TestEffective_TenantCanOnlyTighten(t *testing.T) {
	store, config, tenantID := newPolicyEnv(t)
	for k, v := range map[string]string{
		KeyMinLength:       "16",
		KeyRequireDigit:    "true",
		KeyBlockCommonList: "false",
	} {
		if err := config.Set(t.Context(), tenantID, k, v); err != nil {
			t.Fatalf("Set(%q) error: %v", k, err)
		}
	}

	policy, version, err := store.Effective(t.Context(), tenantID)
	if err != nil {
		t.Fatalf("Effective() error: %v", err)
	}
	if policy.MinLength != 16 || !policy.RequireDigit {
		t.Errorf("policy = %+v, want the tenant's MinLength 16 and RequireDigit", policy)
	}
	if !policy.BlockCommonList {
		t.Error("BlockCommonList = false, want the global true to win over the tenant's false")
	}
	if version != 3 {
		t.Errorf("version = %d, want 3 (one per changed field)", version)
	}
}

func TestEffective_IgnoresInvalidAndContradictoryValues(t *testing.T) {
	store, config, tenantID := newPolicyEnv(t)
	for k, v := range map[string]string{
		KeyMinLength:    "twelve",
		KeyMaxLength:    "8",
		KeyRequireDigit: "maybe",
	} {
		if err := config.Set(t.Context(), tenantID, k, v); err != nil {
			t.Fatalf("Set(%q) error: %v", k, err)
		}
	}

	policy, _, err := store.Effective(t.Context(), tenantID)
	if err != nil {
		t.Fatalf("Effective() error: %v", err)
	}
	if policy != Global {
		t.Errorf("policy = %+v, want Global", policy)
	}
}

func TestEffective_IgnoresTenantMinLengthAboveGlobalMax(t *testing.T) {
	store, config, tenantID := newPolicyEnv(t)
	if err := config.Set(t.Context(), tenantID, KeyMinLength, "200"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	policy, _, err := store.Effective(t.Context(), tenantID)
	if err != nil {
		t.Fatalf("Effective() error: %v", err)
	}
	if policy != Global {
		t.Errorf("policy = %+v, want Global", policy)
	}
}
