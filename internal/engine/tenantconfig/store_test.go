package tenantconfig

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

type testEnv struct {
	store       *Store
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

	store := NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return &testEnv{store: store, tenantStore: tenantStore, conn: conn}
}

func (e *testEnv) createTenant(t *testing.T) *tenant.Tenant {
	t.Helper()
	slug := fmt.Sprintf("tenantconfigtest%d", time.Now().UnixNano())
	tt, err := e.tenantStore.CreateTenant(context.Background(), slug, "Tenant Config Test Co")
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	return tt
}

func TestBootstrap_CreatesTable(t *testing.T) {
	env := openTestEnv(t)

	var tableExists bool
	err := env.conn.QueryRowContext(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'system' AND table_name = 'tenant_config_overrides'
		)
	`).Scan(&tableExists)
	if err != nil {
		t.Fatalf("check table exists: %v", err)
	}
	if !tableExists {
		t.Error("expected system.tenant_config_overrides to exist after Bootstrap()")
	}
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	env := openTestEnv(t)

	if err := env.store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

// TestBootstrap_ConcurrentCallsAllSucceed guards against goerp#171 — see
// tenant.TestBootstrap_ConcurrentCallsAllSucceed's doc comment for what
// this does and doesn't prove.
func TestBootstrap_ConcurrentCallsAllSucceed(t *testing.T) {
	env := openTestEnv(t)

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

func TestSetGet_RoundTrips(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	if err := env.store.Set(context.Background(), tt.ID, "engine.mfa_mode", "required"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	value, ok, err := env.store.Get(context.Background(), tt.ID, "engine.mfa_mode")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if value != "required" {
		t.Errorf("Get() value = %q, want %q", value, "required")
	}
}

func TestGet_UnsetKeyReturnsOkFalse(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	value, ok, err := env.store.Get(context.Background(), tt.ID, "engine.mfa_mode")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if ok {
		t.Error("Get() ok = true for an unset key, want false")
	}
	if value != "" {
		t.Errorf("Get() value = %q for an unset key, want empty", value)
	}
}

func TestSet_UpdatesExistingValue(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	if err := env.store.Set(context.Background(), tt.ID, "engine.mfa_mode", "optional"); err != nil {
		t.Fatalf("first Set() error: %v", err)
	}
	if err := env.store.Set(context.Background(), tt.ID, "engine.mfa_mode", "required"); err != nil {
		t.Fatalf("second Set() error: %v", err)
	}

	value, ok, err := env.store.Get(context.Background(), tt.ID, "engine.mfa_mode")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !ok || value != "required" {
		t.Errorf("Get() = %q, %v, want %q, true", value, ok, "required")
	}
}

func TestSetGet_ScopedPerTenant(t *testing.T) {
	env := openTestEnv(t)
	ttA := env.createTenant(t)
	ttB := env.createTenant(t)

	if err := env.store.Set(context.Background(), ttA.ID, "engine.mfa_mode", "required"); err != nil {
		t.Fatalf("Set() for tenant A error: %v", err)
	}

	_, ok, err := env.store.Get(context.Background(), ttB.ID, "engine.mfa_mode")
	if err != nil {
		t.Fatalf("Get() for tenant B error: %v", err)
	}
	if ok {
		t.Error("Get() for tenant B ok = true, want false — value set on tenant A must not be visible for tenant B")
	}
}

func TestSet_UnknownTenantFails(t *testing.T) {
	env := openTestEnv(t)

	err := env.store.Set(context.Background(), "00000000-0000-0000-0000-000000000000", "engine.mfa_mode", "required")
	if err == nil {
		t.Fatal("expected a foreign key violation for an unknown tenant")
	}
}

func (e *testEnv) version(t *testing.T, tenantID string) string {
	t.Helper()
	v, _, err := e.store.Get(t.Context(), tenantID, PasswordPolicyVersionKey)
	if err != nil {
		t.Fatalf("Get(version) error: %v", err)
	}
	return v
}

func TestSet_PasswordPolicyChangeBumpsVersionOnlyOnRealChange(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	ctx := t.Context()
	key := PasswordPolicyPrefix + "min_length"

	if v := env.version(t, tt.ID); v != "" {
		t.Fatalf("version before any change = %q, want unset", v)
	}
	if err := env.store.Set(ctx, tt.ID, key, "14"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if v := env.version(t, tt.ID); v != "1" {
		t.Errorf("version after first change = %q, want 1", v)
	}
	if err := env.store.Set(ctx, tt.ID, key, "14"); err != nil {
		t.Fatalf("Set() same value error: %v", err)
	}
	if v := env.version(t, tt.ID); v != "1" {
		t.Errorf("version after rewriting the same value = %q, want 1", v)
	}
	if err := env.store.Set(ctx, tt.ID, PasswordPolicyPrefix+"require_digit", "true"); err != nil {
		t.Fatalf("Set() second field error: %v", err)
	}
	if v := env.version(t, tt.ID); v != "2" {
		t.Errorf("version after a second field change = %q, want 2", v)
	}
	if err := env.store.Set(ctx, tt.ID, "engine.mfa_mode", "required"); err != nil {
		t.Fatalf("Set() unrelated key error: %v", err)
	}
	if v := env.version(t, tt.ID); v != "2" {
		t.Errorf("version after an unrelated key = %q, want 2", v)
	}
}

func TestSet_ConcurrentPolicyChangesEachBump(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			errs <- env.store.Set(t.Context(), tt.ID, fmt.Sprintf("%sfield_%d", PasswordPolicyPrefix, i), "true")
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Set() error: %v", err)
		}
	}
	if v := env.version(t, tt.ID); v != fmt.Sprint(n) {
		t.Errorf("version = %q, want %d", v, n)
	}
}

func TestSet_VersionCounterIsReadOnly(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	if err := env.store.Set(t.Context(), tt.ID, PasswordPolicyVersionKey, "99"); !errors.Is(err, ErrReadOnlyKey) {
		t.Errorf("Set(version) error = %v, want ErrReadOnlyKey", err)
	}
}

func TestGetPrefix_ReturnsOnlyMatchingKeys(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	ctx := t.Context()

	for k, v := range map[string]string{
		PasswordPolicyPrefix + "min_length": "14",
		"engine.mfa_mode":                   "required",
	} {
		if err := env.store.Set(ctx, tt.ID, k, v); err != nil {
			t.Fatalf("Set(%q) error: %v", k, err)
		}
	}

	got, err := env.store.GetPrefix(ctx, tt.ID, "auth.password_policy")
	if err != nil {
		t.Fatalf("GetPrefix() error: %v", err)
	}
	want := map[string]string{PasswordPolicyPrefix + "min_length": "14", PasswordPolicyVersionKey: "1"}
	if len(got) != len(want) || got[PasswordPolicyPrefix+"min_length"] != "14" || got[PasswordPolicyVersionKey] != "1" {
		t.Errorf("GetPrefix() = %v, want %v", got, want)
	}
}
