package orm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/wasm's tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func openTestPrimaryDB(t *testing.T) *sql.DB {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func createFixtureTenantSchema(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := tenantschema.Name(slug)

	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	})

	query := fmt.Sprintf(sequencesTableDDL, schemaName)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		t.Fatalf("create sequences table: %v", err)
	}
}

// sequencesTableDDL mirrors internal/engine/tenant/provision/activities.go's
// createSequencesTable — duplicated here rather than imported, since that
// package's constant is unexported and provisioning a real tenant workflow
// is out of scope for these tests.
const sequencesTableDDL = `
CREATE TABLE IF NOT EXISTS %s.sequences (
    model       TEXT NOT NULL,
    field       TEXT NOT NULL,
    period_key  TEXT NOT NULL,
    next_value  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (model, field, period_key)
)
`

func TestAcquireNext_SequentialCallsIncrement(t *testing.T) {
	conn := openTestPrimaryDB(t)
	slug := "seqtest-sequential"
	createFixtureTenantSchema(t, conn, slug)
	ctx := context.Background()

	for want := int64(1); want <= 3; want++ {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		got, err := AcquireNext(ctx, tx, slug, "sales.invoice", "number", "2026")
		if err != nil {
			t.Fatalf("AcquireNext: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if got != want {
			t.Errorf("AcquireNext() = %d, want %d", got, want)
		}
	}
}

func TestAcquireNext_DistinctPeriodKeysAreIndependent(t *testing.T) {
	conn := openTestPrimaryDB(t)
	slug := "seqtest-periods"
	createFixtureTenantSchema(t, conn, slug)
	ctx := context.Background()

	acquire := func(periodKey string) int64 {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		got, err := AcquireNext(ctx, tx, slug, "sales.invoice", "number", periodKey)
		if err != nil {
			t.Fatalf("AcquireNext: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		return got
	}

	if got := acquire("2025"); got != 1 {
		t.Errorf("first 2025 acquisition = %d, want 1", got)
	}
	if got := acquire("2026"); got != 1 {
		t.Errorf("first 2026 acquisition = %d, want 1", got)
	}
	if got := acquire("2025"); got != 2 {
		t.Errorf("second 2025 acquisition = %d, want 2", got)
	}
}

func TestAcquireNext_RollbackLeavesNoGap(t *testing.T) {
	conn := openTestPrimaryDB(t)
	slug := "seqtest-rollback"
	createFixtureTenantSchema(t, conn, slug)
	ctx := context.Background()

	txA, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin txA: %v", err)
	}
	gotA, err := AcquireNext(ctx, txA, slug, "sales.invoice", "number", "2026")
	if err != nil {
		t.Fatalf("AcquireNext (txA): %v", err)
	}
	if err := txA.Rollback(); err != nil {
		t.Fatalf("rollback txA: %v", err)
	}

	txB, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin txB: %v", err)
	}
	gotB, err := AcquireNext(ctx, txB, slug, "sales.invoice", "number", "2026")
	if err != nil {
		t.Fatalf("AcquireNext (txB): %v", err)
	}
	if err := txB.Commit(); err != nil {
		t.Fatalf("commit txB: %v", err)
	}

	if gotA != gotB {
		t.Errorf("rolled-back acquisition %d left a gap: next commit got %d, want %d", gotA, gotB, gotA)
	}
}

func TestAcquireNext_QueryableDirectly(t *testing.T) {
	conn := openTestPrimaryDB(t)
	slug := "seqtest-queryable"
	createFixtureTenantSchema(t, conn, slug)
	ctx := context.Background()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	want, err := AcquireNext(ctx, tx, slug, "sales.invoice", "number", "2026")
	if err != nil {
		t.Fatalf("AcquireNext: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var got int64
	row := conn.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT next_value FROM %s.sequences WHERE model = $1 AND field = $2 AND period_key = $3",
		tenantschema.Name(slug),
	), "sales.invoice", "number", "2026")
	if err := row.Scan(&got); err != nil {
		t.Fatalf("direct query: %v", err)
	}
	if got != want {
		t.Errorf("direct query = %d, want %d", got, want)
	}
}
