package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5/pgconn"
)

// newORMBulkBoundsModuleContext is newORMWriteTestModuleContext with
// ModuleSnapshot.ORMBulkMaxRows/ORMStatementTimeout also set, so these
// tests can exercise a small, fast-to-reach limit instead of the
// production 1000-row/30s defaults.
func newORMBulkBoundsModuleContext(tenantSlug string, modelDecls []model.ModelDeclaration, maxRows int, statementTimeout time.Duration) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, tenantSlug, tenantSlug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{
			ModelDecls:          modelDecls,
			ORMBulkMaxRows:      maxRows,
			ORMStatementTimeout: statementTimeout,
		})
}

func countHardItems(t *testing.T, primaryDB *sql.DB, slug string) int {
	t.Helper()
	var n int
	if err := primaryDB.QueryRow("SELECT count(*) FROM tenant_" + slug + ".hard_item").Scan(&n); err != nil {
		t.Fatalf("count hard_item: %v", err)
	}
	return n
}

func TestORMCreateBatch_OverBulkMaxRows_BatchTooLargeAndNoWrite(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := fmt.Sprintf("ormbulkcreate%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMBulkBoundsModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()}, 3, 0)

	records := make([]map[string]any, 4)
	for i := range records {
		records[i] = map[string]any{"id": uuid.New().String(), "name": fmt.Sprintf("Item %d", i)}
	}

	_, hostErr := ORMCreateBatch(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMCreateBatchInput{
		Model: "testmodule.hard_item", Records: records,
	})
	if hostErr == nil {
		t.Fatal("expected orm.batch_too_large for 4 records over a 3-row limit")
	}
	if hostErr.Code != abi.ErrCodeBatchTooLarge {
		t.Errorf("error code = %v, want %v", hostErr.Code, abi.ErrCodeBatchTooLarge)
	}
	if hostErr.Details["limit"] != 3 || hostErr.Details["count"] != 4 {
		t.Errorf("Details = %#v, want limit=3 count=4", hostErr.Details)
	}
	if got := countHardItems(t, primaryDB, slug); got != 0 {
		t.Errorf("hard_item rows = %d, want 0 (no record written)", got)
	}
}

func TestORMCreateBatch_AtBulkMaxRows_Succeeds(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := fmt.Sprintf("ormbulkcreateok%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMBulkBoundsModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()}, 3, 0)

	records := make([]map[string]any, 3)
	for i := range records {
		records[i] = map[string]any{"id": uuid.New().String(), "name": fmt.Sprintf("Item %d", i)}
	}

	out, hostErr := ORMCreateBatch(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMCreateBatchInput{
		Model: "testmodule.hard_item", Records: records,
	})
	if hostErr != nil {
		t.Fatalf("ORMCreateBatch at the limit: %+v", hostErr)
	}
	if len(out.Records) != 3 {
		t.Errorf("created %d records, want 3", len(out.Records))
	}
	if got := countHardItems(t, primaryDB, slug); got != 3 {
		t.Errorf("hard_item rows = %d, want 3", got)
	}
}

func TestORMWriteMany_OverBulkMaxRows_BatchTooLargeAndNoWrite(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := fmt.Sprintf("ormbulkwritemany%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	ids := make([]string, 4)
	for i := range ids {
		ids[i] = uuid.New().String()
		if _, err := primaryDB.ExecContext(ctx, "INSERT INTO tenant_"+slug+".hard_item (id, name) VALUES ($1, $2)", ids[i], "Original"); err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMBulkBoundsModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()}, 3, 0)

	_, hostErr := ORMWriteMany(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMWriteManyInput{
		Model: "testmodule.hard_item", IDs: ids, Record: map[string]any{"name": "Changed"},
	})
	if hostErr == nil {
		t.Fatal("expected orm.batch_too_large for 4 IDs over a 3-row limit")
	}
	if hostErr.Code != abi.ErrCodeBatchTooLarge {
		t.Errorf("error code = %v, want %v", hostErr.Code, abi.ErrCodeBatchTooLarge)
	}

	var stillOriginal int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".hard_item WHERE name = 'Original'").Scan(&stillOriginal); err != nil {
		t.Fatalf("count unchanged rows: %v", err)
	}
	if stillOriginal != 4 {
		t.Errorf("rows still named Original = %d, want 4 (no record written)", stillOriginal)
	}
}

func TestORMWriteWhere_OverBulkMaxRows_BatchTooLargeAndNoWrite(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := fmt.Sprintf("ormbulkwritewhere%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	for range 4 {
		if _, err := primaryDB.ExecContext(ctx, "INSERT INTO tenant_"+slug+".hard_item (id, name) VALUES ($1, 'Original')", uuid.New().String()); err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMBulkBoundsModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()}, 3, 0)

	_, hostErr := ORMWriteWhere(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMWriteWhereInput{
		Model: "testmodule.hard_item", Domain: "record.name = 'Original'", Record: map[string]any{"name": "Changed"},
	})
	if hostErr == nil {
		t.Fatal("expected orm.batch_too_large for a domain matching 4 rows over a 3-row limit")
	}
	if hostErr.Code != abi.ErrCodeBatchTooLarge {
		t.Errorf("error code = %v, want %v", hostErr.Code, abi.ErrCodeBatchTooLarge)
	}

	var stillOriginal int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".hard_item WHERE name = 'Original'").Scan(&stillOriginal); err != nil {
		t.Fatalf("count unchanged rows: %v", err)
	}
	if stillOriginal != 4 {
		t.Errorf("rows still named Original = %d, want 4 (no record written)", stillOriginal)
	}
}

func TestORMWriteWhere_AtBulkMaxRows_Succeeds(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := fmt.Sprintf("ormbulkwritewhereok%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	for range 3 {
		if _, err := primaryDB.ExecContext(ctx, "INSERT INTO tenant_"+slug+".hard_item (id, name) VALUES ($1, 'Original')", uuid.New().String()); err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMBulkBoundsModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()}, 3, 0)

	out, hostErr := ORMWriteWhere(ctx, r, primaryDB, r.EventInsertClient(), mc, ORMWriteWhereInput{
		Model: "testmodule.hard_item", Domain: "record.name = 'Original'", Record: map[string]any{"name": "Changed"},
	})
	if hostErr != nil {
		t.Fatalf("ORMWriteWhere at the limit: %+v", hostErr)
	}
	if out.Count != 3 {
		t.Errorf("Count = %d, want 3", out.Count)
	}
}

func TestORMUnlink_OverBulkMaxRows_BatchTooLargeAndNoDelete(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := fmt.Sprintf("ormbulkunlink%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	ids := make([]string, 4)
	for i := range ids {
		ids[i] = uuid.New().String()
		if _, err := primaryDB.ExecContext(ctx, "INSERT INTO tenant_"+slug+".hard_item (id, name) VALUES ($1, 'Original')", ids[i]); err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMBulkBoundsModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()}, 3, 0)

	_, hostErr := ORMUnlink(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, ORMUnlinkInput{
		Model: "testmodule.hard_item", IDs: ids,
	})
	if hostErr == nil {
		t.Fatal("expected orm.batch_too_large for 4 IDs over a 3-row limit")
	}
	if hostErr.Code != abi.ErrCodeBatchTooLarge {
		t.Errorf("error code = %v, want %v", hostErr.Code, abi.ErrCodeBatchTooLarge)
	}
	if got := countHardItems(t, primaryDB, slug); got != 4 {
		t.Errorf("hard_item rows = %d, want 4 (no record deleted)", got)
	}
}

// TestORMStatementTimeout_LockContention_ReturnsOrmTimeoutAndRollsBack is
// goerp#898's acceptance criterion: an ORM statement that runs longer
// than GOERP_ORM_STATEMENT_TIMEOUT fails with orm.timeout, and the write
// it was part of is rolled back — a row locked by a concurrent, still-open
// transaction blocks ORMWrite's own UPDATE until Postgres's
// statement_timeout cancels the wait.
func TestORMStatementTimeout_LockContention_ReturnsOrmTimeoutAndRollsBack(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := fmt.Sprintf("ormtimeouttest%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	id := uuid.New().String()
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO tenant_"+slug+".hard_item (id, name) VALUES ($1, 'Original')", id); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	lockConn, err := primaryDB.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire lock conn: %v", err)
	}
	defer func() { _ = lockConn.Close() }()
	lockTx, err := lockConn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin lock tx: %v", err)
	}
	defer func() { _ = lockTx.Rollback() }()
	if _, err := lockTx.ExecContext(ctx, "SELECT 1 FROM tenant_"+slug+".hard_item WHERE id = $1 FOR UPDATE", id); err != nil {
		t.Fatalf("lock row: %v", err)
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMBulkBoundsModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()}, 0, 200*time.Millisecond)

	_, hostErr := ORMWrite(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, ORMWriteInput{
		Model: "testmodule.hard_item", ID: id, Record: map[string]any{"name": "Changed"},
	})
	if hostErr == nil {
		t.Fatal("expected a timeout error while the row was locked, got success")
	}
	if hostErr.Code != abi.ErrCodeORMTimeout {
		t.Errorf("error code = %v, want %v", hostErr.Code, abi.ErrCodeORMTimeout)
	}
	if !hostErr.Retry {
		t.Error("expected Retry to be true for a timeout")
	}

	var name string
	if err := primaryDB.QueryRowContext(ctx, "SELECT name FROM tenant_"+slug+".hard_item WHERE id = $1", id).Scan(&name); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if name != "Original" {
		t.Errorf("name = %q, want %q (the timed-out write must have rolled back)", name, "Original")
	}
}

// TestORMStatementTimeout_DoesNotFireWithinTheTimeout is the negative
// case: a write that finishes well inside GOERP_ORM_STATEMENT_TIMEOUT
// succeeds normally.
func TestORMStatementTimeout_DoesNotFireWithinTheTimeout(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	slug := fmt.Sprintf("ormtimeoutokay%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureHardItemsTable(t, primaryDB, slug)

	id := uuid.New().String()
	if _, err := primaryDB.ExecContext(ctx, "INSERT INTO tenant_"+slug+".hard_item (id, name) VALUES ($1, 'Original')", id); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	r := newHostDBTestRuntime(t, primaryDB, 10)
	mc := newORMBulkBoundsModuleContext(slug, []model.ModelDeclaration{hardDeleteItemModelDecl()}, 0, 5*time.Second)

	_, hostErr := ORMWrite(ctx, r, primaryDB, r.EventInsertClient(), nil, mc, ORMWriteInput{
		Model: "testmodule.hard_item", ID: id, Record: map[string]any{"name": "Changed"},
	})
	if hostErr != nil {
		t.Fatalf("ORMWrite well within the timeout: %+v", hostErr)
	}
}

func TestOrmSQLError_MapsStatementTimeoutSQLState(t *testing.T) {
	err := ormSQLError(&pgconn.PgError{Code: pgStatementTimeoutSQLState, Message: "canceling statement due to statement timeout"})
	if err.Code != abi.ErrCodeORMTimeout {
		t.Errorf("Code = %v, want %v", err.Code, abi.ErrCodeORMTimeout)
	}
	if !err.Retry {
		t.Error("expected Retry to be true")
	}
}

func TestOrmSQLError_OtherErrorsFallBackToUnavailable(t *testing.T) {
	err := ormSQLError(errors.New("connection reset"))
	if err.Code != abi.ErrCodeUnavailable {
		t.Errorf("Code = %v, want %v", err.Code, abi.ErrCodeUnavailable)
	}
}

func TestTranslateWriteError_MapsStatementTimeoutSQLState(t *testing.T) {
	err := translateWriteError(&pgconn.PgError{Code: pgStatementTimeoutSQLState}, hardDeleteItemModelDecl())
	if err.Code != abi.ErrCodeORMTimeout {
		t.Errorf("Code = %v, want %v", err.Code, abi.ErrCodeORMTimeout)
	}
	if !err.Retry {
		t.Error("expected Retry to be true")
	}
}
