package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
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

// TestBootstrap_ConcurrentCallsAllSucceed guards against goerp#171 — see
// schema.TestBootstrap_ConcurrentCallsAllSucceed's doc comment for what
// this does and doesn't prove.
func TestBootstrap_ConcurrentCallsAllSucceed(t *testing.T) {
	store, _ := openTestStore(t)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- store.Bootstrap(context.Background())
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

func TestGetBySlug_Succeeds(t *testing.T) {
	store, conn := openTestStore(t)
	slug := uniqueSlug(t)
	created := createTenant(t, store, conn, slug, "Get Me")

	got, err := store.GetBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetBySlug() error: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
	if got.Name != "Get Me" {
		t.Errorf("Name = %q, want %q", got.Name, "Get Me")
	}
}

func TestGetBySlug_NotFoundReturnsErrTenantNotFound(t *testing.T) {
	store, _ := openTestStore(t)

	_, err := store.GetBySlug(context.Background(), uniqueSlug(t))
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("GetBySlug() error = %v, want ErrTenantNotFound", err)
	}
}

func TestUpdateStatus_SuspendSetsSuspendedAtAndReason(t *testing.T) {
	store, conn := openTestStore(t)
	slug := uniqueSlug(t)
	createTenant(t, store, conn, slug, "Suspend Me")

	reason := "unpaid invoice"
	got, err := store.UpdateStatus(context.Background(), slug, StatusSuspended, &reason)
	if err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}

	if got.Status != StatusSuspended {
		t.Errorf("Status = %q, want %q", got.Status, StatusSuspended)
	}
	if got.SuspendedAt == nil {
		t.Fatal("expected SuspendedAt to be set")
	}
	if got.SuspendReason == nil || *got.SuspendReason != reason {
		t.Errorf("SuspendReason = %v, want %q", got.SuspendReason, reason)
	}
}

func TestUpdateStatus_UnsuspendPreservesSuspensionHistory(t *testing.T) {
	store, conn := openTestStore(t)
	slug := uniqueSlug(t)
	createTenant(t, store, conn, slug, "Unsuspend Me")

	reason := "security review"
	suspended, err := store.UpdateStatus(context.Background(), slug, StatusSuspended, &reason)
	if err != nil {
		t.Fatalf("suspend UpdateStatus() error: %v", err)
	}

	unsuspended, err := store.UpdateStatus(context.Background(), slug, StatusActive, nil)
	if err != nil {
		t.Fatalf("unsuspend UpdateStatus() error: %v", err)
	}

	if unsuspended.Status != StatusActive {
		t.Errorf("Status = %q, want %q", unsuspended.Status, StatusActive)
	}
	if unsuspended.SuspendedAt == nil || !unsuspended.SuspendedAt.Equal(*suspended.SuspendedAt) {
		t.Error("expected SuspendedAt to be preserved across unsuspend, not cleared")
	}
	if unsuspended.SuspendReason == nil || *unsuspended.SuspendReason != reason {
		t.Error("expected SuspendReason to be preserved across unsuspend, not cleared")
	}
}

func TestUpdateStatus_ActivateSetsActivatedAtOnce(t *testing.T) {
	store, conn := openTestStore(t)
	slug := uniqueSlug(t)
	createTenant(t, store, conn, slug, "Activate Me")

	activated, err := store.UpdateStatus(context.Background(), slug, StatusActive, nil)
	if err != nil {
		t.Fatalf("activate UpdateStatus() error: %v", err)
	}
	if activated.ActivatedAt == nil {
		t.Fatal("expected ActivatedAt to be set on first activation")
	}
	firstActivatedAt := *activated.ActivatedAt

	reason := "routine check"
	suspended, err := store.UpdateStatus(context.Background(), slug, StatusSuspended, &reason)
	if err != nil {
		t.Fatalf("suspend UpdateStatus() error: %v", err)
	}
	if suspended.ActivatedAt == nil || !suspended.ActivatedAt.Equal(firstActivatedAt) {
		t.Error("expected ActivatedAt to be preserved across suspend, not cleared")
	}

	reactivated, err := store.UpdateStatus(context.Background(), slug, StatusActive, nil)
	if err != nil {
		t.Fatalf("reactivate UpdateStatus() error: %v", err)
	}
	if reactivated.ActivatedAt == nil || !reactivated.ActivatedAt.Equal(firstActivatedAt) {
		t.Error("expected ActivatedAt to stay at the original activation time, not reset on reactivation")
	}
}

func TestUpdateStatus_NotFoundReturnsErrTenantNotFound(t *testing.T) {
	store, _ := openTestStore(t)

	_, err := store.UpdateStatus(context.Background(), uniqueSlug(t), StatusActive, nil)
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("UpdateStatus() error = %v, want ErrTenantNotFound", err)
	}
}

func TestList_FiltersByStatusAndPlan(t *testing.T) {
	store, conn := openTestStore(t)
	ctx := context.Background()

	active := createTenant(t, store, conn, uniqueSlug(t), "Active Plan Test")
	if _, err := store.db.ExecContext(ctx, "UPDATE system.tenants SET status = 'active' WHERE id = $1", active.ID); err != nil {
		t.Fatalf("mark tenant active: %v", err)
	}
	provisioning := createTenant(t, store, conn, uniqueSlug(t), "Still Provisioning")

	got, _, err := store.List(ctx, ListFilter{Status: StatusActive})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	gotSlugs := make(map[string]bool, len(got))
	for _, tt := range got {
		gotSlugs[tt.Slug] = true
	}
	if !gotSlugs[active.Slug] {
		t.Errorf("expected %q in List(status=active) result", active.Slug)
	}
	if gotSlugs[provisioning.Slug] {
		t.Errorf("did not expect provisioning tenant %q in List(status=active) result", provisioning.Slug)
	}
}

func TestList_PaginatesWithCursor(t *testing.T) {
	store, conn := openTestStore(t)
	ctx := context.Background()

	var created []*Tenant
	for range 3 {
		created = append(created, createTenant(t, store, conn, uniqueSlug(t), "Page Test"))
	}

	page1, cursor1, err := store.List(ctx, ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("List() page 1 error: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("len(page1) = %d, want 2", len(page1))
	}
	if cursor1 == "" {
		t.Fatal("expected a non-empty cursor after a full first page")
	}

	page2, _, err := store.List(ctx, ListFilter{Limit: 2, Cursor: cursor1})
	if err != nil {
		t.Fatalf("List() page 2 error: %v", err)
	}

	seen := make(map[string]bool)
	for _, tt := range page1 {
		seen[tt.ID] = true
	}
	for _, tt := range page2 {
		if seen[tt.ID] {
			t.Errorf("tenant %q appeared on both pages", tt.Slug)
		}
	}
	_ = created
}

func TestGetByID_Succeeds(t *testing.T) {
	store, conn := openTestStore(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Get By ID")

	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.Slug != created.Slug {
		t.Errorf("Slug = %q, want %q", got.Slug, created.Slug)
	}
}

func TestGetByID_NotFoundReturnsErrTenantNotFound(t *testing.T) {
	store, _ := openTestStore(t)

	_, err := store.GetByID(context.Background(), "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("GetByID() error = %v, want ErrTenantNotFound", err)
	}
}

func TestGetByID_DeletedTenantReturnsErrTenantNotFound(t *testing.T) {
	store, conn := openTestStore(t)
	ctx := context.Background()
	created := createTenant(t, store, conn, uniqueSlug(t), "Deleted Tenant")

	if _, err := store.db.ExecContext(ctx, "UPDATE system.tenants SET status = 'deleted' WHERE id = $1", created.ID); err != nil {
		t.Fatalf("mark tenant deleted: %v", err)
	}

	_, err := store.GetByID(ctx, created.ID)
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("GetByID() error = %v, want ErrTenantNotFound", err)
	}
}

// insertDomain inserts a tenant_domains row for tenantID, cleaned up via
// tenantID's own cascading deleteTenant registration — no separate
// cleanup needed.
func insertDomain(t *testing.T, conn *sql.DB, tenantID, domain string, domainType DomainType, verified bool) {
	t.Helper()
	var verifiedAt any
	if verified {
		verifiedAt = time.Now()
	}
	_, err := conn.ExecContext(context.Background(), `
		INSERT INTO system.tenant_domains (tenant_id, domain, type, verified_at)
		VALUES ($1, $2, $3, $4)
	`, tenantID, domain, string(domainType), verifiedAt)
	if err != nil {
		t.Fatalf("insert domain %q for tenant %q: %v", domain, tenantID, err)
	}
}

func TestGetByDomain_ResolvesSubdomainWithoutVerification(t *testing.T) {
	store, conn := openTestStore(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Subdomain Tenant")
	domain := created.Slug + ".example.com"
	insertDomain(t, conn, created.ID, domain, DomainSubdomain, false)

	got, err := store.GetByDomain(context.Background(), domain)
	if err != nil {
		t.Fatalf("GetByDomain() error: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
}

func TestGetByDomain_UnverifiedCustomDomainNotResolved(t *testing.T) {
	store, conn := openTestStore(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Unverified Custom")
	domain := uniqueSlug(t) + ".customdomain.test"
	insertDomain(t, conn, created.ID, domain, DomainCustom, false)

	_, err := store.GetByDomain(context.Background(), domain)
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("GetByDomain() error = %v, want ErrTenantNotFound for an unverified custom domain", err)
	}
}

func TestGetByDomain_VerifiedCustomDomainResolves(t *testing.T) {
	store, conn := openTestStore(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Verified Custom")
	domain := uniqueSlug(t) + ".customdomain.test"
	insertDomain(t, conn, created.ID, domain, DomainCustom, true)

	got, err := store.GetByDomain(context.Background(), domain)
	if err != nil {
		t.Fatalf("GetByDomain() error: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
}

func TestGetByDomain_NotFoundReturnsErrTenantNotFound(t *testing.T) {
	store, _ := openTestStore(t)

	_, err := store.GetByDomain(context.Background(), "nonexistent-"+uniqueSlug(t)+".example.com")
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("GetByDomain() error = %v, want ErrTenantNotFound", err)
	}
}

func TestGetByDomain_DeletedTenantReturnsErrTenantNotFound(t *testing.T) {
	store, conn := openTestStore(t)
	ctx := context.Background()
	created := createTenant(t, store, conn, uniqueSlug(t), "Deleted Domain Tenant")
	domain := created.Slug + ".example.com"
	insertDomain(t, conn, created.ID, domain, DomainSubdomain, false)

	if _, err := store.db.ExecContext(ctx, "UPDATE system.tenants SET status = 'deleted' WHERE id = $1", created.ID); err != nil {
		t.Fatalf("mark tenant deleted: %v", err)
	}

	_, err := store.GetByDomain(ctx, domain)
	if !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("GetByDomain() error = %v, want ErrTenantNotFound", err)
	}
}

func TestDomainsForTenant_ReturnsVerifiedAndUnverified(t *testing.T) {
	store, conn := openTestStore(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "Domains For Tenant")
	insertDomain(t, conn, created.ID, created.Slug+".example.com", DomainSubdomain, false)
	insertDomain(t, conn, created.ID, created.Slug+".custom.test", DomainCustom, true)

	got, err := store.DomainsForTenant(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("DomainsForTenant() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(DomainsForTenant()) = %d, want 2", len(got))
	}
}

func TestDomainsForTenant_NoDomainsReturnsEmpty(t *testing.T) {
	store, conn := openTestStore(t)
	created := createTenant(t, store, conn, uniqueSlug(t), "No Domains")

	got, err := store.DomainsForTenant(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("DomainsForTenant() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(DomainsForTenant()) = %d, want 0", len(got))
	}
}
