package recordshares

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
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

	var uniqueDef string
	if err := conn.QueryRow(
		"SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = 'record_shares' AND indexname = 'idx_record_shares_unique'",
		"tenant_"+slug,
	).Scan(&uniqueDef); err != nil {
		t.Fatalf("read idx_record_shares_unique: %v", err)
	}
	if !strings.Contains(uniqueDef, "UNIQUE") || !strings.Contains(uniqueDef, "(model, record_id, shared_with_user_id)") {
		t.Errorf("idx_record_shares_unique = %q, want a unique index on (model, record_id, shared_with_user_id)", uniqueDef)
	}

	var lookupExists bool
	if err := conn.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND tablename = 'record_shares' AND indexname = 'idx_record_shares_lookup')",
		"tenant_"+slug,
	).Scan(&lookupExists); err != nil {
		t.Fatalf("check idx_record_shares_lookup: %v", err)
	}
	if lookupExists {
		t.Error("idx_record_shares_lookup should not exist: the unique index covers the same columns")
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

const (
	testModel = "sales.order"
	recordA   = "11111111-1111-1111-1111-111111111111"
	userX     = "22222222-2222-2222-2222-222222222222"
	userY     = "33333333-3333-3333-3333-333333333333"
	sharerP   = "44444444-4444-4444-4444-444444444444"
	sharerQ   = "55555555-5555-5555-5555-555555555555"
)

func TestBootstrap_RejectsASecondRowForTheSameRecipientAndRecord(t *testing.T) {
	_, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	insert := "INSERT INTO " + schema + ".record_shares (model, record_id, shared_with_user_id, permission, shared_by) VALUES ($1, $2, $3, 'read', $4)"

	if _, err := conn.Exec(insert, testModel, recordA, userX, sharerP); err != nil {
		t.Fatalf("first insert error: %v", err)
	}
	if _, err := conn.Exec(insert, testModel, recordA, userX, sharerQ); err == nil {
		t.Fatal("second insert for the same (model, record_id, shared_with_user_id) succeeded, want a unique violation")
	}
	if _, err := conn.Exec(insert, testModel, recordA, userY, sharerP); err != nil {
		t.Errorf("insert for a different recipient error: %v", err)
	}
}

func TestGrant_InsertsThenUpdatesInPlace(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := t.Context()

	first, created, err := store.Grant(ctx, slug, testModel, recordA, userX, "read", sharerP, nil)
	if err != nil {
		t.Fatalf("first Grant() error: %v", err)
	}
	if !created {
		t.Error("first Grant() created = false, want true")
	}

	expires := time.Now().Add(48 * time.Hour).Truncate(time.Microsecond)
	second, created, err := store.Grant(ctx, slug, testModel, recordA, userX, "write", sharerQ, &expires)
	if err != nil {
		t.Fatalf("second Grant() error: %v", err)
	}
	if created {
		t.Error("second Grant() created = true, want false for an existing recipient")
	}
	if second.ID != first.ID || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("second Grant() id/created_at = %s/%v, want the original %s/%v", second.ID, second.CreatedAt, first.ID, first.CreatedAt)
	}
	if second.Permission != "write" || second.SharedBy != sharerQ || second.ExpiresAt == nil || !second.ExpiresAt.Equal(expires) {
		t.Errorf("second Grant() = %+v, want permission write, shared_by %s, expires_at %v", second, sharerQ, expires)
	}

	shares, err := store.ListForRecord(ctx, slug, testModel, recordA)
	if err != nil {
		t.Fatalf("ListForRecord() error: %v", err)
	}
	if len(shares) != 1 {
		t.Errorf("shares after two grants to one recipient = %d, want 1", len(shares))
	}
}

func TestGrant_RenewingAnExpiredShareClearsItsExpiry(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := t.Context()

	past := time.Now().Add(-time.Hour)
	if _, _, err := store.Grant(ctx, slug, testModel, recordA, userX, "read", sharerP, &past); err != nil {
		t.Fatalf("Grant() with a past expiry error: %v", err)
	}
	if _, created, err := store.Grant(ctx, slug, testModel, recordA, userX, "read", sharerP, nil); err != nil || created {
		t.Fatalf("Grant() renewing error = %v, created = %v, want nil, false", err, created)
	}

	shares, err := store.ListForRecord(ctx, slug, testModel, recordA)
	if err != nil {
		t.Fatalf("ListForRecord() error: %v", err)
	}
	if len(shares) != 1 || shares[0].ExpiresAt != nil {
		t.Errorf("shares = %+v, want one non-expiring share", shares)
	}
}

func TestGrant_ConcurrentCallsForOneRecipientLeaveOneRow(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)

	const callers = 12
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	var createdCount int
	var mu sync.Mutex
	for range callers {
		wg.Go(func() {
			_, created, err := store.Grant(t.Context(), slug, testModel, recordA, userX, "read", sharerP, nil)
			if err != nil {
				errs <- err
				return
			}
			if created {
				mu.Lock()
				createdCount++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Grant() error: %v", err)
	}

	var rows int
	if err := conn.QueryRow("SELECT COUNT(*) FROM " + schema + ".record_shares").Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 || createdCount != 1 {
		t.Errorf("rows = %d, created = %d after %d concurrent grants, want exactly one of each", rows, createdCount, callers)
	}
}

// createLegacyRecordShares builds record_shares the way it existed before
// the unique index: the same columns and only the non-unique lookup index.
func createLegacyRecordShares(t *testing.T, conn *sql.DB, schema string) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE ` + schema + `.record_shares (
		    id                   UUID PRIMARY KEY DEFAULT uuidv7(),
		    model                TEXT NOT NULL,
		    record_id            UUID NOT NULL,
		    shared_with_user_id  UUID NOT NULL,
		    permission           TEXT NOT NULL CHECK (permission IN ('read', 'write')),
		    shared_by            UUID NOT NULL,
		    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    expires_at           TIMESTAMPTZ
		)`,
		`CREATE INDEX idx_record_shares_lookup ON ` + schema + `.record_shares(model, record_id, shared_with_user_id)`,
	} {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("create legacy record_shares: %v", err)
		}
	}
}

// seedLegacyDuplicates inserts three rows for (recordA, userX) — oldest to
// newest, permissions read, write, read — and two for userY: an older grant
// that is still active and a newer one that has already expired.
func seedLegacyDuplicates(t *testing.T, conn *sql.DB, schema string) {
	t.Helper()
	insert := "INSERT INTO " + schema + `.record_shares (id, model, record_id, shared_with_user_id, permission, shared_by, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	base := time.Now().Add(-72 * time.Hour)
	expired := time.Now().Add(-time.Hour)
	rows := []struct {
		id, user, permission string
		age                  time.Duration
		expiresAt            *time.Time
	}{
		{"aaaaaaaa-0000-0000-0000-000000000001", userX, "read", 0, nil},
		{"aaaaaaaa-0000-0000-0000-000000000002", userX, "write", time.Hour, nil},
		{"aaaaaaaa-0000-0000-0000-000000000003", userX, "read", 2 * time.Hour, nil},
		{"aaaaaaaa-0000-0000-0000-000000000004", userY, "write", 3 * time.Hour, nil},
		{"aaaaaaaa-0000-0000-0000-000000000005", userY, "read", 4 * time.Hour, &expired},
	}
	for _, r := range rows {
		if _, err := conn.Exec(insert, r.id, testModel, recordA, r.user, r.permission, sharerP, base.Add(r.age), r.expiresAt); err != nil {
			t.Fatalf("seed legacy row %s: %v", r.id, err)
		}
	}
}

func remainingShareIDs(t *testing.T, conn *sql.DB, schema string) []string {
	t.Helper()
	rows, err := conn.Query("SELECT id FROM " + schema + ".record_shares ORDER BY id")
	if err != nil {
		t.Fatalf("list share ids: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestBootstrap_DeduplicatesAnExistingTableKeepingOneRowPerRecipient(t *testing.T) {
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("recordsharesmigrate%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE") })
	createLegacyRecordShares(t, conn, schema)
	seedLegacyDuplicates(t, conn, schema)

	store := NewStore(conn)
	if err := store.Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("Bootstrap() over a table holding duplicates error: %v", err)
	}

	want := []string{"aaaaaaaa-0000-0000-0000-000000000003", "aaaaaaaa-0000-0000-0000-000000000004"} // userX: newest; userY: the still-active row over the newer expired one
	if got := remainingShareIDs(t, conn, schema); !slices.Equal(got, want) {
		t.Errorf("remaining share ids = %v, want the newest row per recipient %v", got, want)
	}

	var lookupExists, uniqueExists bool
	if err := conn.QueryRow(`SELECT
		EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND indexname = 'idx_record_shares_lookup'),
		EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND indexname = 'idx_record_shares_unique')`, "tenant_"+slug).Scan(&lookupExists, &uniqueExists); err != nil {
		t.Fatalf("check indexes: %v", err)
	}
	if lookupExists || !uniqueExists {
		t.Errorf("lookup index exists = %v, unique index exists = %v, want false, true", lookupExists, uniqueExists)
	}

	if err := store.Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("second Bootstrap() error: %v", err)
	}
	if got := remainingShareIDs(t, conn, schema); !slices.Equal(got, want) {
		t.Errorf("share ids after a second Bootstrap() = %v, want unchanged %v", got, want)
	}
}
