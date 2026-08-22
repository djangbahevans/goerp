package enforce

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

type testEnv struct {
	store       *Store
	config      *tenantconfig.Store
	tenantStore *tenant.Store
	conn        *sql.DB
}

func openTestEnv(t *testing.T) *testEnv {
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
	configStore := tenantconfig.NewStore(conn)
	if err := configStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}

	return &testEnv{
		store:       NewStore(configStore),
		config:      configStore,
		tenantStore: tenantStore,
		conn:        conn,
	}
}

func (e *testEnv) createTenant(t *testing.T) *tenant.Tenant {
	t.Helper()
	slug := fmt.Sprintf("enforcetest%d", time.Now().UnixNano())
	tt, err := e.tenantStore.CreateTenant(context.Background(), slug, "Enforce Test Co")
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	return tt
}

func TestLoadPolicy_DefaultsWhenUnconfigured(t *testing.T) {
	e := openTestEnv(t)
	tt := e.createTenant(t)

	policy, err := e.store.LoadPolicy(context.Background(), tt.ID)
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}
	if policy.Mode != ModeOptional {
		t.Errorf("Mode = %q, want %q", policy.Mode, ModeOptional)
	}
	if policy.MaxAssuranceAge != DefaultMaxAssuranceAge {
		t.Errorf("MaxAssuranceAge = %v, want %v", policy.MaxAssuranceAge, DefaultMaxAssuranceAge)
	}
	if len(policy.RequiredRoles) != 0 {
		t.Errorf("RequiredRoles = %v, want empty", policy.RequiredRoles)
	}
}

func TestLoadPolicy_ReadsConfiguredMode(t *testing.T) {
	e := openTestEnv(t)
	tt := e.createTenant(t)
	ctx := context.Background()

	if err := e.config.Set(ctx, tt.ID, "mfa.enforcement_mode", "required"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	policy, err := e.store.LoadPolicy(ctx, tt.ID)
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}
	if policy.Mode != ModeRequired {
		t.Errorf("Mode = %q, want %q", policy.Mode, ModeRequired)
	}
}

func TestLoadPolicy_UnrecognizedModeDefaultsToOptional(t *testing.T) {
	e := openTestEnv(t)
	tt := e.createTenant(t)
	ctx := context.Background()

	if err := e.config.Set(ctx, tt.ID, "mfa.enforcement_mode", "not-a-real-mode"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	policy, err := e.store.LoadPolicy(ctx, tt.ID)
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}
	if policy.Mode != ModeOptional {
		t.Errorf("Mode = %q, want %q (fallback for an unrecognized value)", policy.Mode, ModeOptional)
	}
}

func TestLoadPolicy_ReadsConfiguredMaxAssuranceAge(t *testing.T) {
	e := openTestEnv(t)
	tt := e.createTenant(t)
	ctx := context.Background()

	if err := e.config.Set(ctx, tt.ID, "mfa.max_assurance_age_hours", "2"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	policy, err := e.store.LoadPolicy(ctx, tt.ID)
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}
	if policy.MaxAssuranceAge != 2*time.Hour {
		t.Errorf("MaxAssuranceAge = %v, want 2h", policy.MaxAssuranceAge)
	}
}

func TestLoadPolicy_UnparseableMaxAssuranceAgeDefaultsTo24h(t *testing.T) {
	e := openTestEnv(t)
	tt := e.createTenant(t)
	ctx := context.Background()

	if err := e.config.Set(ctx, tt.ID, "mfa.max_assurance_age_hours", "not-a-number"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	policy, err := e.store.LoadPolicy(ctx, tt.ID)
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}
	if policy.MaxAssuranceAge != DefaultMaxAssuranceAge {
		t.Errorf("MaxAssuranceAge = %v, want default %v", policy.MaxAssuranceAge, DefaultMaxAssuranceAge)
	}
}

func TestLoadPolicy_ReadsRequiredRolesOnlyForRequiredForRolesMode(t *testing.T) {
	e := openTestEnv(t)
	tt := e.createTenant(t)
	ctx := context.Background()

	if err := e.config.Set(ctx, tt.ID, "mfa.enforcement_mode", "required_for_roles"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if err := e.config.Set(ctx, tt.ID, "mfa.required_roles", "admin, finance_manager"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	policy, err := e.store.LoadPolicy(ctx, tt.ID)
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}
	if len(policy.RequiredRoles) != 2 || policy.RequiredRoles[0] != "admin" || policy.RequiredRoles[1] != "finance_manager" {
		t.Errorf("RequiredRoles = %v, want [admin finance_manager]", policy.RequiredRoles)
	}
}

func TestLoadPolicy_RequiredRolesIgnoredForOtherModes(t *testing.T) {
	e := openTestEnv(t)
	tt := e.createTenant(t)
	ctx := context.Background()

	if err := e.config.Set(ctx, tt.ID, "mfa.enforcement_mode", "required"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if err := e.config.Set(ctx, tt.ID, "mfa.required_roles", "admin"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	policy, err := e.store.LoadPolicy(ctx, tt.ID)
	if err != nil {
		t.Fatalf("LoadPolicy() error: %v", err)
	}
	if len(policy.RequiredRoles) != 0 {
		t.Errorf("RequiredRoles = %v, want empty for ModeRequired (roles list only applies to required_for_roles)", policy.RequiredRoles)
	}
}
