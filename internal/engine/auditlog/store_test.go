package auditlog

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance (bypassing PgBouncer), same convention as tenant.Store's tests.
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

func TestBootstrap_CreatesCreatedAtIndex(t *testing.T) {
	store, _ := openTestStore(t)

	var indexDef string
	err := store.db.QueryRowContext(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'system' AND indexname = 'idx_admin_audit_log_created_at'`,
	).Scan(&indexDef)
	if err != nil {
		t.Fatalf("expected idx_admin_audit_log_created_at to exist: %v", err)
	}
	if indexDef == "" {
		t.Fatal("expected a non-empty index definition")
	}
}

func TestWrite_RoundTripsAllFields(t *testing.T) {
	store, conn := openTestStore(t)
	ctx := context.Background()

	row := Row{
		OperatorIdentity: "operator-cn-jane",
		Endpoint:         "POST /admin/tenants/{slug}/suspend",
		TargetScope:      "acme",
		IdempotencyKey:   "idem-123",
		JobID:            "job_abc",
		StatusCode:       202,
	}
	if err := store.Write(ctx, row); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	var got Row
	err := conn.QueryRowContext(ctx, `
		SELECT operator_identity, endpoint, target_scope, COALESCE(idempotency_key, ''), COALESCE(job_id, ''), status_code
		FROM system.admin_audit_log
		WHERE endpoint = $1 AND target_scope = $2
	`, row.Endpoint, row.TargetScope).Scan(&got.OperatorIdentity, &got.Endpoint, &got.TargetScope, &got.IdempotencyKey, &got.JobID, &got.StatusCode)
	if err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), `DELETE FROM system.admin_audit_log WHERE endpoint = $1 AND target_scope = $2`, row.Endpoint, row.TargetScope)
	})

	if got != row {
		t.Errorf("round-tripped row = %+v, want %+v", got, row)
	}
}

func TestWrite_EmptyIdempotencyKeyAndJobIDStoreAsNull(t *testing.T) {
	store, conn := openTestStore(t)
	ctx := context.Background()

	row := Row{
		OperatorIdentity: "internal",
		Endpoint:         "POST /admin/tenants",
		TargetScope:      "acme2",
		StatusCode:       200,
	}
	if err := store.Write(ctx, row); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), `DELETE FROM system.admin_audit_log WHERE endpoint = $1 AND target_scope = $2`, row.Endpoint, row.TargetScope)
	})

	var idemNull, jobNull bool
	err := conn.QueryRowContext(ctx, `
		SELECT idempotency_key IS NULL, job_id IS NULL
		FROM system.admin_audit_log
		WHERE endpoint = $1 AND target_scope = $2
	`, row.Endpoint, row.TargetScope).Scan(&idemNull, &jobNull)
	if err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if !idemNull {
		t.Error("expected idempotency_key to be NULL when not sent, not an empty string")
	}
	if !jobNull {
		t.Error("expected job_id to be NULL for a non-202 write, not an empty string")
	}
}
