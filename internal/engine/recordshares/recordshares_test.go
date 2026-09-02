package recordshares

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/role's tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// openTestStore creates a fixture tenant_<random> schema directly (this
// package's tests don't wait on real tenant provisioning to exist — same
// reasoning role_test.go's own openTestStore already established) and
// returns a Store plus that schema's slug for tests to target.
func openTestStore(t *testing.T) (store *Store, conn *sql.DB, tenantSlug string) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("recordsharestest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)

	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	store = NewStore(conn)
	if err := store.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store, conn, slug
}

func TestBootstrap_CreatesTableAndIndex(t *testing.T) {
	_, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)

	var tableExists bool
	if err := conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'record_shares')",
		"tenant_"+slug,
	).Scan(&tableExists); err != nil {
		t.Fatalf("check record_shares table: %v", err)
	}
	if !tableExists {
		t.Fatal("expected record_shares table to exist after Bootstrap()")
	}

	var indexExists bool
	if err := conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND tablename = 'record_shares' AND indexname = 'idx_record_shares_lookup')",
		"tenant_"+slug,
	).Scan(&indexExists); err != nil {
		t.Fatalf("check idx_record_shares_lookup: %v", err)
	}
	if !indexExists {
		t.Error("expected idx_record_shares_lookup to exist after Bootstrap()")
	}

	// Zero rows immediately after provisioning — nothing else in this
	// ticket reads or writes it.
	var count int
	if err := conn.QueryRow("SELECT COUNT(*) FROM " + schema + ".record_shares").Scan(&count); err != nil {
		t.Fatalf("count record_shares rows: %v", err)
	}
	if count != 0 {
		t.Errorf("record_shares row count = %d, want 0", count)
	}
}

func TestBootstrap_PermissionCheckRejectsInvalidValue(t *testing.T) {
	_, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)

	_, err := conn.Exec(
		"INSERT INTO " + schema + ".record_shares (model, record_id, shared_with_user_id, permission, shared_by) " +
			"VALUES ('sales.order', gen_random_uuid(), gen_random_uuid(), 'delete', gen_random_uuid())",
	)
	if err == nil {
		t.Fatal("expected the permission CHECK constraint to reject a value outside ('read', 'write')")
	}
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _, slug := openTestStore(t)

	if err := store.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

// TestBootstrap_ConcurrentCallsAgainstFreshSchemaAllSucceed guards
// against goerp#171 directly — N concurrent first-time Bootstrap calls
// racing on CREATE TABLE/INDEX IF NOT EXISTS against objects that don't
// exist yet, the same failure mode role_test.go's own equivalent test
// guards against. Uses its own fresh schema (not openTestStore's) so
// this is the case under test, and per-test unique schemas make this
// safe alongside every other test/package touching Postgres
// concurrently.
func TestBootstrap_ConcurrentCallsAgainstFreshSchemaAllSucceed(t *testing.T) {
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("recordsharesconcurrent%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	store := NewStore(conn)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- store.Bootstrap(context.Background(), slug)
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
