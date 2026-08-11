package tenant

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance (bypassing PgBouncer, same convention as
// internal/engine/schema's tests — see that package's localSchemaSyncDSN).
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openTestStore(t *testing.T) *Store {
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
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.tenant_domains")
		_, _ = conn.ExecContext(context.Background(), "DELETE FROM system.tenants")
	})

	return store
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
	store := openTestStore(t)

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
	store := openTestStore(t)

	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

func TestCreateTenant_Succeeds(t *testing.T) {
	store := openTestStore(t)
	slug := uniqueSlug(t)

	got, err := store.CreateTenant(context.Background(), slug, "Acme Corp")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}

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
	store := openTestStore(t)
	slug := uniqueSlug(t)

	if _, err := store.CreateTenant(context.Background(), slug, "First"); err != nil {
		t.Fatalf("first CreateTenant() error: %v", err)
	}

	_, err := store.CreateTenant(context.Background(), slug, "Second")
	if err == nil {
		t.Fatal("expected an error creating a tenant with a duplicate slug")
	}
}

func TestCreateTenant_InvalidSlugFormatFails(t *testing.T) {
	store := openTestStore(t)

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
	store := openTestStore(t)

	_, err := store.db.ExecContext(context.Background(), `
		INSERT INTO system.tenant_domains (tenant_id, domain)
		VALUES ('00000000-0000-0000-0000-000000000000', 'nonexistent.example.com')
	`)
	if err == nil {
		t.Fatal("expected a foreign key violation inserting a domain for a nonexistent tenant")
	}
}
