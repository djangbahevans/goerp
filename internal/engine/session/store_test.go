package session

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance (bypassing PgBouncer), same convention as auditlog.Store's
// tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

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

func TestBootstrap_CreatesSessionsTableWithAllColumns(t *testing.T) {
	_, conn := openTestStore(t)

	wantColumns := []string{
		"id", "user_id", "tenant_id", "family_id", "device_id", "refresh_hash",
		"user_agent", "ip_address", "country_code", "created_at", "last_active_at",
		"expires_at", "rotated_at", "revoked_at", "revoke_reason",
		"mfa_verified_at", "mfa_method", "mfa_credential_id",
	}
	for _, col := range wantColumns {
		var exists bool
		err := conn.QueryRowContext(context.Background(), `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'system' AND table_name = 'sessions' AND column_name = $1
			)
		`, col).Scan(&exists)
		if err != nil {
			t.Fatalf("query column %q: %v", col, err)
		}
		if !exists {
			t.Errorf("expected column %q to exist on system.sessions", col)
		}
	}
}

func TestBootstrap_CreatesRefreshHashPartialIndex(t *testing.T) {
	_, conn := openTestStore(t)

	var indexDef string
	err := conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_sessions_refresh_hash'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_sessions_refresh_hash to exist: %v", err)
	}
	if !containsAll(indexDef, "revoked_at", "rotated_at") {
		t.Errorf("expected idx_sessions_refresh_hash to be a partial index on revoked_at/rotated_at, got: %s", indexDef)
	}
}

func TestBootstrap_CreatesUserPartialIndex(t *testing.T) {
	_, conn := openTestStore(t)

	var indexDef string
	err := conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_sessions_user'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_sessions_user to exist: %v", err)
	}
	if !containsAll(indexDef, "user_id", "tenant_id", "revoked_at") {
		t.Errorf("expected idx_sessions_user to cover user_id/tenant_id, partial on revoked_at, got: %s", indexDef)
	}
}

func TestBootstrap_CreatesFamilyIndex(t *testing.T) {
	_, conn := openTestStore(t)

	var indexDef string
	err := conn.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_sessions_family'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_sessions_family to exist: %v", err)
	}
	if !containsAll(indexDef, "family_id") {
		t.Errorf("expected idx_sessions_family to cover family_id, got: %s", indexDef)
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
