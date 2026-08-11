package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance (bypassing PgBouncer, same convention as
// internal/engine/schema's tests — see that package's localSchemaSyncDSN).
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// openTestStore returns a Store plus its underlying connection, so callers
// that create tenants can clean up exactly the rows they created (see
// deleteTenant below) — NOT a blanket "DELETE FROM system.tenants" on
// every test's cleanup. system.tenants is a real, shared table on a real
// Postgres instance with no per-test isolation, and Go runs different
// packages' test binaries concurrently by default: a blanket wipe here
// would race with (and had raced with) any other package's tests —
// internal/engine/tenantsync's, in particular — relying on their own
// tenant rows surviving for the duration of their own test.
func openTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := NewStore(conn)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store, conn
}

// deleteTenant removes exactly the given tenant (and, via tenant_domains'
// ON DELETE CASCADE, any domains it owns) — the scoped cleanup every test
// that creates a tenant registers via t.Cleanup.
func deleteTenant(t *testing.T, conn *sql.DB, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.tenants WHERE id = $1", id)
	})
}

// createTenant creates a tenant, fails the test on error, and registers
// its scoped cleanup in one call — the pattern every test below that
// needs a real row follows.
func createTenant(t *testing.T, store *Store, conn *sql.DB, slug, name string) *Tenant {
	t.Helper()
	tt, err := store.CreateTenant(context.Background(), slug, name)
	if err != nil {
		t.Fatalf("CreateTenant(%q, %q) error: %v", slug, name, err)
	}
	deleteTenant(t, conn, tt.ID)
	return tt
}

// uniqueSlug keeps each test's inserted rows from colliding with a
// previous run's leftovers or a concurrently-running test — Bootstrap's
// tables persist across test runs (no per-test schema teardown), so a
// fixed slug like "acme" would eventually collide with itself.
func uniqueSlug(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("t%d", time.Now().UnixNano())
}

func TestBootstrap_CreatesTablesAndIndex(t *testing.T) {
	store, _ := openTestStore(t)

	var indexDef string
	err := store.db.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_tenant_domains_domain'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_tenant_domains_domain to exist: %v", err)
	}
	if indexDef == "" {
		t.Fatal("expected a non-empty index definition")
	}
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _ := openTestStore(t)

	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

func TestCreateTenant_Succeeds(t *testing.T) {
	store, conn := openTestStore(t)
	slug := uniqueSlug(t)

	got := createTenant(t, store, conn, slug, "Acme Corp")

	if got.ID == "" {
		t.Error("expected a generated ID")
	}
	if got.Slug != slug {
		t.Errorf("Slug = %q, want %q", got.Slug, slug)
	}
	if got.Name != "Acme Corp" {
		t.Errorf("Name = %q, want %q", got.Name, "Acme Corp")
	}
	if got.Plan != PlanStarter {
		t.Errorf("Plan = %q, want default %q", got.Plan, PlanStarter)
	}
	if got.Status != StatusProvisioning {
		t.Errorf("Status = %q, want default %q", got.Status, StatusProvisioning)
	}
	if got.Region != "default" {
		t.Errorf("Region = %q, want default %q", got.Region, "default")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("expected CreatedAt/UpdatedAt to be set")
	}
}

func TestCreateTenant_DuplicateSlugFails(t *testing.T) {
	store, conn := openTestStore(t)
	slug := uniqueSlug(t)

	createTenant(t, store, conn, slug, "First")

	_, err := store.CreateTenant(context.Background(), slug, "Second")
	if err == nil {
		t.Fatal("expected an error creating a tenant with a duplicate slug")
	}
}

func TestCreateTenant_InvalidSlugFormatFails(t *testing.T) {
	store, _ := openTestStore(t)

	cases := []string{
		"",           // empty
		"Acme",       // uppercase
		"1acme",      // must start with a letter
		"a",          // too short (min 3 chars per the regex)
		"has spaces", // spaces not allowed
		"trailing-",  // must end in alphanumeric
	}
	for _, slug := range cases {
		if _, err := store.CreateTenant(context.Background(), slug, "Test"); err == nil {
			t.Errorf("CreateTenant(%q): expected an error, got none", slug)
		}
	}
}

func TestTenantDomainsForeignKey_RejectsUnknownTenant(t *testing.T) {
	store, _ := openTestStore(t)

	_, err := store.db.ExecContext(context.Background(), `
		INSERT INTO system.tenant_domains (tenant_id, domain)
		VALUES ('00000000-0000-0000-0000-000000000000', 'nonexistent.example.com')
	`)
	if err == nil {
		t.Fatal("expected a foreign key violation inserting a domain for a nonexistent tenant")
	}
}

func TestActiveTenants_ReturnsOnlyActiveStatus(t *testing.T) {
	store, conn := openTestStore(t)
	ctx := context.Background()

	active1 := createTenant(t, store, conn, uniqueSlug(t), "Active One")
	active2 := createTenant(t, store, conn, uniqueSlug(t), "Active Two")
	provisioning := createTenant(t, store, conn, uniqueSlug(t), "Still Provisioning")

	for _, id := range []string{active1.ID, active2.ID} {
		if _, err := store.db.ExecContext(ctx, "UPDATE system.tenants SET status = 'active' WHERE id = $1", id); err != nil {
			t.Fatalf("mark tenant active: %v", err)
		}
	}
	_ = provisioning // left at its default "provisioning" status deliberately

	got, err := store.ActiveTenants(ctx)
	if err != nil {
		t.Fatalf("ActiveTenants() error: %v", err)
	}

	gotSlugs := make(map[string]bool, len(got))
	for _, tt := range got {
		if tt.Status != StatusActive {
			t.Errorf("ActiveTenants() returned tenant %q with status %q, want %q", tt.Slug, tt.Status, StatusActive)
		}
		gotSlugs[tt.Slug] = true
	}

	if !gotSlugs[active1.Slug] {
		t.Errorf("expected %q in ActiveTenants() result", active1.Slug)
	}
	if !gotSlugs[active2.Slug] {
		t.Errorf("expected %q in ActiveTenants() result", active2.Slug)
	}
	if gotSlugs[provisioning.Slug] {
		t.Errorf("did not expect provisioning tenant %q in ActiveTenants() result", provisioning.Slug)
	}
}
