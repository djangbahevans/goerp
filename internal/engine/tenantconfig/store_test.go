package tenantconfig

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

type testEnv struct {
	store       *Store
	tenantStore *tenant.Store
	conn        *sql.DB
}

func openTestEnv(t *testing.T) *testEnv {
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

	store := NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return &testEnv{store: store, tenantStore: tenantStore, conn: conn}
}

func (e *testEnv) createTenant(t *testing.T) *tenant.Tenant {
	t.Helper()
	slug := fmt.Sprintf("tenantconfigtest%d", time.Now().UnixNano())
	tt, err := e.tenantStore.CreateTenant(t.Context(), slug, "Tenant Config Test Co")
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	return tt
}

func TestBootstrap_CreatesTable(t *testing.T) {
	env := openTestEnv(t)

	var tableExists bool
	err := env.conn.QueryRowContext(t.Context(), `
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

	if err := env.store.Bootstrap(t.Context()); err != nil {
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
			errs <- env.store.Bootstrap(t.Context())
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

	if err := env.store.Set(t.Context(), tt.ID, "engine.mfa_mode", "required"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	value, ok, err := env.store.Get(t.Context(), tt.ID, "engine.mfa_mode")
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

	value, ok, err := env.store.Get(t.Context(), tt.ID, "engine.mfa_mode")
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

	if err := env.store.Set(t.Context(), tt.ID, "engine.mfa_mode", "optional"); err != nil {
		t.Fatalf("first Set() error: %v", err)
	}
	if err := env.store.Set(t.Context(), tt.ID, "engine.mfa_mode", "required"); err != nil {
		t.Fatalf("second Set() error: %v", err)
	}

	value, ok, err := env.store.Get(t.Context(), tt.ID, "engine.mfa_mode")
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

	if err := env.store.Set(t.Context(), ttA.ID, "engine.mfa_mode", "required"); err != nil {
		t.Fatalf("Set() for tenant A error: %v", err)
	}

	_, ok, err := env.store.Get(t.Context(), ttB.ID, "engine.mfa_mode")
	if err != nil {
		t.Fatalf("Get() for tenant B error: %v", err)
	}
	if ok {
		t.Error("Get() for tenant B ok = true, want false — value set on tenant A must not be visible for tenant B")
	}
}

func TestSet_UnknownTenantFails(t *testing.T) {
	env := openTestEnv(t)

	err := env.store.Set(t.Context(), "00000000-0000-0000-0000-000000000000", "engine.mfa_mode", "required")
	if err == nil {
		t.Fatal("expected a foreign key violation for an unknown tenant")
	}
}

func (e *testEnv) changedAt(t *testing.T, tenantID string) string {
	t.Helper()
	v, _, err := e.store.Get(t.Context(), tenantID, PasswordPolicyChangedAtKey)
	if err != nil {
		t.Fatalf("Get(changed_at) error: %v", err)
	}
	return v
}

func TestSet_PasswordPolicyChangeStampsChangedAtOnlyOnRealChange(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	ctx := t.Context()

	if v := env.changedAt(t, tt.ID); v != "" {
		t.Fatalf("changed_at before any change = %q, want unset", v)
	}
	if err := env.store.Set(ctx, tt.ID, PasswordPolicyMinLengthKey, "14"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	first := env.changedAt(t, tt.ID)
	if _, err := time.Parse(time.RFC3339Nano, first); err != nil {
		t.Fatalf("changed_at after first change = %q, want an RFC 3339 time: %v", first, err)
	}
	if err := env.store.Set(ctx, tt.ID, PasswordPolicyMinLengthKey, "14"); err != nil {
		t.Fatalf("Set() same value error: %v", err)
	}
	if v := env.changedAt(t, tt.ID); v != first {
		t.Errorf("changed_at after rewriting the same value = %q, want %q", v, first)
	}
	if err := env.store.Set(ctx, tt.ID, PasswordPolicyGraceDaysKey, "30"); err != nil {
		t.Fatalf("Set() grace_days error: %v", err)
	}
	if v := env.changedAt(t, tt.ID); v != first {
		t.Errorf("changed_at after a grace_days change = %q, want %q", v, first)
	}
	if err := env.store.Set(ctx, tt.ID, "engine.mfa_mode", "required"); err != nil {
		t.Fatalf("Set() unrelated key error: %v", err)
	}
	if v := env.changedAt(t, tt.ID); v != first {
		t.Errorf("changed_at after an unrelated key = %q, want %q", v, first)
	}
	if err := env.store.Set(ctx, tt.ID, PasswordPolicyEnforcementKey, "require"); err != nil {
		t.Fatalf("Set() enforcement error: %v", err)
	}
	if v := env.changedAt(t, tt.ID); v <= first {
		t.Errorf("changed_at after an enforcement change = %q, want later than %q", v, first)
	}
}

func TestSet_ConcurrentPolicyChangesAllLand(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			key := PasswordPolicyMinLengthKey
			if i%2 == 1 {
				key = PasswordPolicyEnforcementKey
			}
			errs <- env.store.Set(t.Context(), tt.ID, key, fmt.Sprint(i))
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Set() error: %v", err)
		}
	}
	if v := env.changedAt(t, tt.ID); v == "" {
		t.Error("changed_at unset after concurrent changes")
	}
}

func TestSet_ChangedAtIsReadOnly(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)

	if err := env.store.Set(t.Context(), tt.ID, PasswordPolicyChangedAtKey, "2020-01-01T00:00:00Z"); !errors.Is(err, ErrReadOnlyKey) {
		t.Errorf("Set(changed_at) error = %v, want ErrReadOnlyKey", err)
	}
}

func TestGetPrefix_ReturnsOnlyMatchingKeys(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	ctx := t.Context()

	for k, v := range map[string]string{
		PasswordPolicyMinLengthKey: "14",
		"engine.mfa_mode":          "required",
	} {
		if err := env.store.Set(ctx, tt.ID, k, v); err != nil {
			t.Fatalf("Set(%q) error: %v", k, err)
		}
	}

	got, err := env.store.GetPrefix(ctx, tt.ID, PasswordPolicyPrefix)
	if err != nil {
		t.Fatalf("GetPrefix() error: %v", err)
	}
	if len(got) != 2 || got[PasswordPolicyMinLengthKey] != "14" || got[PasswordPolicyChangedAtKey] == "" {
		t.Errorf("GetPrefix() = %v, want min_length 14 and changed_at", got)
	}
}

func TestSetModuleConfig_UpsertsAndIsVisibleThroughResolver(t *testing.T) {
	env := openTestEnv(t)
	tt := env.createTenant(t)
	env.createModuleConfigSchema(t, tt.Slug)
	ctx := t.Context()

	reg := testRegistryWithSeed("contacts", "default_country_code", "US")
	resolver := NewResolver(env.store, env.tenantStore, reg)
	tenantSchema := tenantschema.Name(tt.Slug)

	if err := env.store.SetModuleConfig(ctx, tt.ID, tenantSchema, "contacts", "default_country_code", []byte(`"FR"`), "string", false, ""); err != nil {
		t.Fatalf("SetModuleConfig() error: %v", err)
	}
	// Store.Set's own pg_notify only reaches this Resolver asynchronously
	// through a running Listener — this test has none, so it invalidates
	// directly, the same way host.config.set itself does synchronously
	// right after a successful write (internal/engine/wasm/host_config.go)
	// to satisfy this ticket's own no-stale-read AC (goerp#1283).
	resolver.Invalidate(tt.ID, "contacts.default_country_code")

	// A read through the Resolver immediately after Set must observe the
	// new value, never a stale cache entry — this ticket's own AC
	// (goerp#1283): "A set followed immediately by a get ... observes the
	// new value — no stale read from the generation-counted cache."
	value, _, ok, err := resolver.Get(ctx, tt.ID, "contacts.default_country_code")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !ok || value != "FR" {
		t.Fatalf("Get() = %q, %v, want %q, true", value, ok, "FR")
	}

	// A second Set (the ON CONFLICT DO UPDATE path) upserts rather than
	// erroring, and the Resolver again observes the change with no stale
	// read.
	if err := env.store.SetModuleConfig(ctx, tt.ID, tenantSchema, "contacts", "default_country_code", []byte(`"DE"`), "string", false, ""); err != nil {
		t.Fatalf("SetModuleConfig() upsert error: %v", err)
	}
	resolver.Invalidate(tt.ID, "contacts.default_country_code")
	value, _, ok, err = resolver.Get(ctx, tt.ID, "contacts.default_country_code")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !ok || value != "DE" {
		t.Fatalf("Get() after upsert = %q, %v, want %q, true", value, ok, "DE")
	}

	var encrypted bool
	row := env.conn.QueryRowContext(ctx, fmt.Sprintf("SELECT encrypted FROM %s.module_config WHERE module_name = $1 AND key = $2", tenantSchema), "contacts", "default_country_code")
	if err := row.Scan(&encrypted); err != nil {
		t.Fatalf("scan encrypted column: %v", err)
	}
	if encrypted {
		t.Errorf("encrypted = true, want false")
	}
}
