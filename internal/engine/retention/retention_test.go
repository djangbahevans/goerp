package retention

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5"
)

type entitlements map[string]bool

func (e entitlements) LoadCurrentEntitlements(_ context.Context, id string) (tenantresolve.EntitlementSet, error) {
	enabled, ok := e[id]
	if !ok {
		return tenantresolve.EntitlementSet{}, errors.New("entitlements unavailable")
	}

	return tenantresolve.EntitlementSet{Features: map[string]bool{"module.retentiontest": enabled}}, nil
}

type fixture struct {
	sweeper *Sweeper
	pool    *sql.DB
	tenant  tenant.Tenant
	other   tenant.Tenant
	mod     *module.LoadedModule
	at      time.Time
}

func newFixture(t *testing.T, policies ...manifest.RetentionPolicy) *fixture {
	t.Helper()
	admin := membershiptest.New(t)
	var database string
	if err := admin.QueryRowContext(t.Context(), "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	pool := membershiptest.Open(t, database, "schema_sync_user")
	store := tenant.NewStore(pool)

	var tenants []tenant.Tenant
	for _, prefix := range []string{"retentionone", "retentiontwo"} {
		slug := prefix + strings.TrimPrefix(database, "membership")
		tn, err := store.CreateTenant(t.Context(), slug, slug)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateStatus(t.Context(), slug, tenant.StatusActive, nil); err != nil {
			t.Fatal(err)
		}
		if err := tenantschema.Create(t.Context(), pool, slug); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := tenantschema.Drop(context.Background(), pool, slug); err != nil {
				t.Errorf("drop retention fixture tenant: %v", err)
			}
		})

		for _, table := range []string{"retention_item", "explicit_items", "other_items", "virtual_items", "transient_items"} {
			qualified := pgx.Identifier{"tenant_" + slug, table}.Sanitize()
			if _, err := pool.ExecContext(t.Context(), "CREATE TABLE "+qualified+" (id int PRIMARY KEY, created_at timestamptz NOT NULL)"); err != nil {
				t.Fatal(err)
			}
		}
		tenants = append(tenants, *tn)
	}

	mod := &module.LoadedModule{
		Status:   module.StatusReady,
		Manifest: manifest.Manifest{Name: "retentiontest", RetentionPolicies: policies},
		ModelDecls: []model.ModelDeclaration{
			{Name: "RetentionItem"},
			{Name: "Overridden", Table: "explicit_items"},
			{Name: "VirtualItem", Table: "virtual_items", Backend: model.BackendVirtual},
			{Name: "TransientItem", Table: "transient_items", Backend: model.BackendTransient},
		},
	}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"retentiontest": mod,
		"other": {
			Status:     module.StatusReady,
			Manifest:   manifest.Manifest{Name: "other"},
			ModelDecls: []model.ModelDeclaration{{Name: "OtherItem", Table: "other_items"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	return &fixture{
		pool:   pool,
		tenant: tenants[0],
		other:  tenants[1],
		mod:    mod,
		at:     time.Date(2026, 10, 12, 12, 0, 0, 0, time.FixedZone("offset", 2*60*60)),
		sweeper: &Sweeper{
			Pool:         pool,
			Registry:     reg,
			Tenants:      store,
			Entitlements: entitlements{tenants[0].ID: true, tenants[1].ID: false},
		},
	}
}

func (f *fixture) seed(t *testing.T, slug, table string) {
	t.Helper()
	qualified := pgx.Identifier{"tenant_" + slug, table}.Sanitize()
	cutoff := f.at.Add(-30 * 24 * time.Hour)
	if _, err := f.pool.ExecContext(t.Context(), "INSERT INTO "+qualified+" VALUES (1, $1), (2, $2), (3, $3)",
		cutoff.Add(-time.Microsecond), cutoff, cutoff.Add(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) ids(t *testing.T, slug, table string) []int {
	t.Helper()
	qualified := pgx.Identifier{"tenant_" + slug, table}.Sanitize()
	rows, err := f.pool.QueryContext(t.Context(), "SELECT id FROM "+qualified+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	return ids
}

func TestSweepPreviewDeletionBoundariesAndIsolation(t *testing.T) {
	f := newFixture(t,
		manifest.RetentionPolicy{Table: "retention_item", RetainDays: 30, Action: "delete"},
		manifest.RetentionPolicy{Table: "explicit_items", RetainDays: 30, Action: "delete"},
	)
	for _, tn := range []tenant.Tenant{f.tenant, f.other} {
		for _, table := range []string{"retention_item", "explicit_items"} {
			f.seed(t, tn.Slug, table)
		}
	}

	qualified := pgx.Identifier{"tenant_" + f.tenant.Slug, "retention_item"}.Sanitize()
	if _, err := f.pool.ExecContext(t.Context(), "ALTER TABLE "+qualified+" ENABLE ROW LEVEL SECURITY"); err != nil {
		t.Fatal(err)
	}

	for run, preview := range []bool{true, false, false, true} {
		result, err := f.sweeper.Run(t.Context(), f.at, preview)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Policies) != 2 {
			t.Fatalf("policies = %+v, want only enabled tenant's two policies", result.Policies)
		}

		for _, p := range result.Policies {
			wantStatus := "deleted"
			if preview {
				wantStatus = "previewed"
			}
			if p.Status != wantStatus || p.TenantID != f.tenant.ID || !p.Cutoff.Equal(f.at.Add(-30*24*time.Hour)) {
				t.Errorf("policy = %+v", p)
			}
			wantAffected := int64(1)
			if run >= 2 {
				wantAffected = 0
			}
			if p.Affected != wantAffected {
				t.Errorf("affected = %d, want %d", p.Affected, wantAffected)
			}

			want := []int{2, 3}
			if run == 0 {
				want = []int{1, 2, 3}
			}
			if got := f.ids(t, f.tenant.Slug, p.Table); !slices.Equal(got, want) {
				t.Errorf("enabled tenant rows = %v, want %v", got, want)
			}
			if got := f.ids(t, f.other.Slug, p.Table); !slices.Equal(got, []int{1, 2, 3}) {
				t.Errorf("disabled tenant rows = %v, want all rows intact", got)
			}
		}
	}
}

func TestSweepSkipsUnsupportedAndInvalidPolicies(t *testing.T) {
	base := manifest.RetentionPolicy{Table: "retention_item", RetainDays: 30, Action: "delete"}
	var policies []manifest.RetentionPolicy
	for _, condition := range []string{"record.id = 1", " ", "invalid("} {
		p := base
		p.Condition = condition
		policies = append(policies, p)
	}
	for _, action := range []string{"archive", "nullify", "bogus", ""} {
		p := base
		p.Action = action
		policies = append(policies, p)
	}
	for _, days := range []int{0, -1, math.MaxInt} {
		p := base
		p.RetainDays = days
		policies = append(policies, p)
	}
	for _, table := range []string{"", "other_items", "virtual_items", "transient_items", "undeclared", "tenant_members", "retention_item; DELETE FROM other_items", "retention_item\x00", strings.Repeat("a", 64)} {
		p := base
		p.Table = table
		policies = append(policies, p)
	}
	f := newFixture(t, policies...)
	for _, table := range []string{"retention_item", "other_items", "virtual_items", "transient_items"} {
		f.seed(t, f.tenant.Slug, table)
	}

	for _, preview := range []bool{true, false} {
		result, err := f.sweeper.Run(t.Context(), f.at, preview)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Policies) != len(policies) {
			t.Fatalf("got %d policy results, want %d", len(result.Policies), len(policies))
		}
		for i, p := range result.Policies {
			if p.Policy != i || p.Status != "skipped" || p.Diagnostic == "" || p.Affected != 0 {
				t.Errorf("policy result = %+v", p)
			}
		}
		for _, table := range []string{"retention_item", "other_items", "virtual_items", "transient_items"} {
			if got := f.ids(t, f.tenant.Slug, table); !slices.Equal(got, []int{1, 2, 3}) {
				t.Errorf("rows in %s = %v, want all rows intact", table, got)
			}
		}
	}
}

func TestPreviewDisallowedDoesNotPreventDeletion(t *testing.T) {
	f := newFixture(t, manifest.RetentionPolicy{Table: "retention_item", RetainDays: 30, Action: "delete", DryRunSafe: new(false)})
	f.seed(t, f.tenant.Slug, "retention_item")

	result, err := f.sweeper.Run(t.Context(), f.at, true)
	if err != nil {
		t.Fatal(err)
	}
	if p := result.Policies[0]; p.Status != "skipped" || p.Diagnostic != "policy disallows preview" {
		t.Errorf("preview result = %+v", p)
	}
	if got := f.ids(t, f.tenant.Slug, "retention_item"); len(got) != 3 {
		t.Errorf("preview mutated rows: %v", got)
	}

	result, err = f.sweeper.Run(t.Context(), f.at, false)
	if err != nil {
		t.Fatal(err)
	}
	if p := result.Policies[0]; p.Status != "deleted" || p.Affected != 1 {
		t.Errorf("deletion result = %+v", p)
	}
}

func TestSweepFailureDoesNotStopOtherPoliciesOrTenants(t *testing.T) {
	f := newFixture(t,
		manifest.RetentionPolicy{Table: "retention_item", RetainDays: 30, Action: "delete"},
		manifest.RetentionPolicy{Table: "explicit_items", RetainDays: 30, Action: "delete"},
	)
	f.seed(t, f.tenant.Slug, "explicit_items")
	f.seed(t, f.other.Slug, "explicit_items")
	f.sweeper.Entitlements = entitlements{f.tenant.ID: true, f.other.ID: true}
	if _, err := f.pool.ExecContext(t.Context(), "DROP TABLE "+pgx.Identifier{"tenant_" + f.tenant.Slug, "retention_item"}.Sanitize()); err != nil {
		t.Fatal(err)
	}

	result, err := f.sweeper.Run(t.Context(), f.at, false)
	if err == nil || len(result.Policies) != 4 {
		t.Fatalf("result = %+v, error = %v, want partial failure", result, err)
	}
	for _, p := range result.Policies {
		if p.Table == "explicit_items" && (p.Status != "deleted" || p.Affected != 1) {
			t.Errorf("healthy policy = %+v", p)
		}
		if p.Table == "retention_item" && p.TenantID == f.tenant.ID && (p.Status != "failed" || p.Diagnostic == "") {
			t.Errorf("failed policy = %+v", p)
		}
	}
}

func TestSweepEntitlementsFailClosed(t *testing.T) {
	f := newFixture(t, manifest.RetentionPolicy{Table: "retention_item", RetainDays: 30, Action: "delete"})
	f.seed(t, f.tenant.Slug, "retention_item")
	f.seed(t, f.other.Slug, "retention_item")
	f.sweeper.Entitlements = entitlements{f.other.ID: true}

	result, err := f.sweeper.Run(t.Context(), f.at, false)
	if err == nil || len(result.Policies) != 1 || result.Policies[0].TenantID != f.other.ID {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	if got := f.ids(t, f.tenant.Slug, "retention_item"); len(got) != 3 {
		t.Errorf("failed admission mutated rows: %v", got)
	}
}

func TestSweepRequiresSnapshotAndTime(t *testing.T) {
	s := &Sweeper{Registry: &registry.ModuleRegistry{}}
	if _, err := s.Run(t.Context(), time.Now(), false); err == nil {
		t.Fatal("missing snapshot succeeded")
	}
	if _, err := s.Run(t.Context(), time.Time{}, false); err == nil {
		t.Fatal("missing sweep time succeeded")
	}
}

func TestSweepSkipsInactiveTenantsAndUnreadyModules(t *testing.T) {
	f := newFixture(t, manifest.RetentionPolicy{Table: "retention_item", RetainDays: 30, Action: "delete"})
	f.seed(t, f.tenant.Slug, "retention_item")
	store := tenant.NewStore(f.pool)
	if _, err := store.UpdateStatus(t.Context(), f.tenant.Slug, tenant.StatusSuspended, nil); err != nil {
		t.Fatal(err)
	}

	result, err := f.sweeper.Run(t.Context(), f.at, false)
	if err != nil || len(result.Policies) != 0 {
		t.Fatalf("inactive tenant result = %+v, error = %v", result, err)
	}
	if got := f.ids(t, f.tenant.Slug, "retention_item"); len(got) != 3 {
		t.Errorf("inactive tenant rows changed: %v", got)
	}

	if _, err := store.UpdateStatus(t.Context(), f.tenant.Slug, tenant.StatusActive, nil); err != nil {
		t.Fatal(err)
	}
	for _, status := range []module.ModuleStatus{module.StatusFailed, module.StatusSyncing} {
		unready := *f.mod
		unready.Status = status
		if _, err := f.sweeper.Registry.Update(map[string]*module.LoadedModule{"retentiontest": &unready}); err != nil {
			t.Fatal(err)
		}

		result, err := f.sweeper.Run(t.Context(), f.at, false)
		if err != nil || len(result.Policies) != 0 {
			t.Fatalf("unready module result = %+v, error = %v", result, err)
		}
		if got := f.ids(t, f.tenant.Slug, "retention_item"); len(got) != 3 {
			t.Errorf("unready module rows changed: %v", got)
		}
	}
}
