package password

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

// fakeMembers stands in for role.Store: tenants lists the account's
// tenants, joined their join times by slug.
type fakeMembers struct {
	tenants []string
	joined  map[string]time.Time
	err     error
}

func (f *fakeMembers) CandidateTenantIDs(context.Context, string) ([]string, error) {
	return f.tenants, f.err
}

func (f *fakeMembers) JoinedAt(_ context.Context, tenantSlug, _ string) (time.Time, error) {
	if f.err != nil {
		return time.Time{}, f.err
	}
	return f.joined[tenantSlug], nil
}

type policyEnv struct {
	conn    *sql.DB
	store   *PolicyStore
	config  *tenantconfig.Store
	tenants *tenant.Store
	members *fakeMembers
}

func newPolicyEnv(t *testing.T) *policyEnv {
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

	members := &fakeMembers{joined: map[string]time.Time{}}
	return &policyEnv{conn: conn, store: NewPolicyStore(config, members), config: config, tenants: tenantStore, members: members}
}

func (e *policyEnv) createTenant(t *testing.T) *tenant.Tenant {
	t.Helper()
	tt, err := e.tenants.CreateTenant(t.Context(), fmt.Sprintf("pwpolicytest%d", time.Now().UnixNano()), "Password Policy Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	return tt
}

func (e *policyEnv) set(t *testing.T, tenantID string, values map[string]string) {
	t.Helper()
	if err := e.config.SetMany(t.Context(), tenantID, values); err != nil {
		t.Fatalf("SetMany(%v) error: %v", values, err)
	}
}

func TestTenant_UnconfiguredTenantGetsDefaults(t *testing.T) {
	env := newPolicyEnv(t)
	tt := env.createTenant(t)

	p, err := env.store.Tenant(t.Context(), tt.ID)
	if err != nil {
		t.Fatalf("Tenant() error: %v", err)
	}
	want := TenantPolicy{MinLength: Global.MinLength, Enforcement: EnforcementNudge, GraceDays: DefaultGraceDays}
	if p != want {
		t.Errorf("Tenant() = %+v, want %+v", p, want)
	}
}

func TestTenant_ReadsSettingsAndChangedAt(t *testing.T) {
	env := newPolicyEnv(t)
	tt := env.createTenant(t)
	env.set(t, tt.ID, map[string]string{KeyMinLength: "16", KeyEnforcement: "require", KeyGraceDays: "30"})

	p, err := env.store.Tenant(t.Context(), tt.ID)
	if err != nil {
		t.Fatalf("Tenant() error: %v", err)
	}
	if p.MinLength != 16 || p.Enforcement != EnforcementRequire || p.GraceDays != 30 {
		t.Errorf("Tenant() = %+v, want min 16, require, 30 days", p)
	}
	if p.ChangedAt == nil || time.Since(*p.ChangedAt) > time.Minute {
		t.Errorf("ChangedAt = %v, want the time of the write", p.ChangedAt)
	}
}

func TestTenant_IgnoresOutOfRangeAndInvalidValues(t *testing.T) {
	env := newPolicyEnv(t)
	tt := env.createTenant(t)
	env.set(t, tt.ID, map[string]string{KeyMinLength: "21", KeyEnforcement: "block", KeyGraceDays: "91"})

	p, err := env.store.Tenant(t.Context(), tt.ID)
	if err != nil {
		t.Fatalf("Tenant() error: %v", err)
	}
	if p.MinLength != Global.MinLength || p.Enforcement != EnforcementNudge || p.GraceDays != DefaultGraceDays {
		t.Errorf("Tenant() = %+v, want the defaults", p)
	}

	env.set(t, tt.ID, map[string]string{KeyMinLength: "8", KeyGraceDays: "soon"})
	if p, _ = env.store.Tenant(t.Context(), tt.ID); p.MinLength != Global.MinLength || p.GraceDays != DefaultGraceDays {
		t.Errorf("Tenant() = %+v, want the defaults", p)
	}
}

func TestMinLength_TenantOrGlobal(t *testing.T) {
	env := newPolicyEnv(t)
	tt := env.createTenant(t)
	if got := env.store.MinLength(t.Context(), ""); got != Global.MinLength {
		t.Errorf("MinLength(no tenant) = %d, want %d", got, Global.MinLength)
	}
	env.set(t, tt.ID, map[string]string{KeyMinLength: "16"})
	if got := env.store.MinLength(t.Context(), tt.ID); got != 16 {
		t.Errorf("MinLength(tenant) = %d, want 16", got)
	}
}

func TestCombinedMinLength_LargestAmongMembershipsAndJoiningTenant(t *testing.T) {
	env := newPolicyEnv(t)
	a, b, joining := env.createTenant(t), env.createTenant(t), env.createTenant(t)
	env.set(t, a.ID, map[string]string{KeyMinLength: "14"})
	env.set(t, b.ID, map[string]string{KeyMinLength: "16"})
	env.set(t, joining.ID, map[string]string{KeyMinLength: "18"})
	env.members.tenants = []string{a.ID, b.ID}

	cases := []struct {
		name    string
		userID  string
		joining string
		want    int
	}{
		{"no account, no tenant", "", "", Global.MinLength},
		{"account's tenants", "user", "", 16},
		{"joining a stricter tenant", "user", joining.ID, 18},
		{"new account joining", "", a.ID, 14},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := env.store.CombinedMinLength(t.Context(), tc.userID, tc.joining)
			if err != nil {
				t.Fatalf("CombinedMinLength() error: %v", err)
			}
			if got != tc.want {
				t.Errorf("CombinedMinLength() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCombinedMinLength_MembershipLookupFailureFails(t *testing.T) {
	env := newPolicyEnv(t)
	env.members.err = errors.New("boom")
	if _, err := env.store.CombinedMinLength(t.Context(), "user", ""); err == nil {
		t.Error("CombinedMinLength() error = nil, want the lookup failure")
	}
}

func TestCheckSignIn_AppliesTenantEnforcementAndJoinTime(t *testing.T) {
	env := newPolicyEnv(t)
	tt := env.createTenant(t)
	env.set(t, tt.ID, map[string]string{KeyMinLength: "16", KeyEnforcement: "require", KeyGraceDays: "0"})
	const short = "qzvkplmwxtrb"

	env.members.joined[tt.Slug] = time.Now().Add(-time.Hour)
	if got := env.store.CheckSignIn(t.Context(), tt.ID, tt.Slug, "user", short, "kwame@example.com"); got.Outcome != ChangeRequired {
		t.Errorf("CheckSignIn() = %+v, want required once a zero-day grace period is over", got)
	}

	env.set(t, tt.ID, map[string]string{KeyGraceDays: "14"})
	got := env.store.CheckSignIn(t.Context(), tt.ID, tt.Slug, "user", short, "kwame@example.com")
	if got.Outcome != UpdateRecommended || got.Deadline == nil {
		t.Errorf("CheckSignIn() = %+v, want recommended with a deadline within the grace period", got)
	}

	env.members.joined[tt.Slug] = time.Now().Add(time.Minute)
	if got := env.store.CheckSignIn(t.Context(), tt.ID, tt.Slug, "user", short, "kwame@example.com"); got.Outcome != ChangeRequired {
		t.Errorf("CheckSignIn() = %+v, want required for a member who joined after the change", got)
	}
}

func TestSave_ValidatesAndStampsChangedAt(t *testing.T) {
	env := newPolicyEnv(t)
	tt := env.createTenant(t)

	for _, bad := range []TenantPolicy{
		{MinLength: 11, Enforcement: EnforcementNudge, GraceDays: 14},
		{MinLength: 21, Enforcement: EnforcementNudge, GraceDays: 14},
		{MinLength: 14, Enforcement: "block", GraceDays: 14},
		{MinLength: 14, Enforcement: EnforcementNudge, GraceDays: 91},
	} {
		if err := env.store.Save(t.Context(), tt.ID, bad); !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("Save(%+v) error = %v, want ErrInvalidSetting", bad, err)
		}
	}

	want := TenantPolicy{MinLength: 14, Enforcement: EnforcementRequire, GraceDays: 7}
	if err := env.store.Save(t.Context(), tt.ID, want); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	got, err := env.store.Tenant(t.Context(), tt.ID)
	if err != nil {
		t.Fatalf("Tenant() error: %v", err)
	}
	if got.MinLength != 14 || got.Enforcement != EnforcementRequire || got.GraceDays != 7 || got.ChangedAt == nil {
		t.Errorf("Tenant() = %+v, want the saved settings with ChangedAt", got)
	}
}
