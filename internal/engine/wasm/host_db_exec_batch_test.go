package wasm

import (
	"context"
	"fmt"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/abi"
)

// TestDBExecBatch_CumulativeBatchTime_ExceedsPerRowTimeout_StillCommits is
// a regression test for a bug caught in code review: DBExecBatch's own
// transaction must not be bound to any single row's own per-row timeout
// window. opts.timeout_ms (300ms) here is a per-row budget only — each
// row's own 50ms delay stays comfortably within it — but the batch's
// cumulative wall time (20 rows x 50ms = 1000ms+) safely exceeds it. If
// the transaction's own BeginTx were still bound to that 300ms window
// (the bug — database/sql ties a transaction's whole lifetime to the
// context BeginTx was called with, not just the BeginTx call itself),
// Postgres would auto-rollback the whole transaction partway through and
// later rows would fail with "sql: transaction has already been
// committed or rolled back" — confirmed by temporarily reintroducing the
// bug against this exact test before fixing it for real.
func TestDBExecBatch_CumulativeBatchTime_ExceedsPerRowTimeout_StillCommits(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	const n = 20
	ids := make([]string, n)
	paramSets := make([][]any, n)
	for i := range n {
		id := fmt.Sprintf("20000012-0000-0000-0000-%012d", i+1)
		ids[i] = id
		if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL: "INSERT INTO gadget (id, name) VALUES ($1, $2)", Params: []any{id, "Before"},
		}); hostErr != nil {
			t.Fatalf("seed insert %d: %+v", i, hostErr)
		}
		paramSets[i] = []any{"After", id}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "UPDATE gadget SET name = $1 WHERE id = $2 AND (SELECT pg_sleep(0.05)) IS NOT NULL",
		ParamSets: paramSets,
		Opts:      dbExecBatchOpts{TimeoutMs: 300},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != n {
		t.Errorf("TotalRowsAffected = %d, want %d", out.TotalRowsAffected, n)
	}

	for _, id := range ids {
		var name string
		if err := primaryDB.QueryRowContext(ctx, "SELECT name FROM tenant_"+mc.TenantSlug+".gadget WHERE id = $1", id).Scan(&name); err != nil {
			t.Fatalf("scan %s: %v", id, err)
		}
		if name != "After" {
			t.Errorf("row %s not committed (name = %q): the batch transaction was likely auto-rolled-back mid-batch", id, name)
		}
	}
}

func TestDBExecBatch_Insert_AllSucceed(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		ParamSets: [][]any{
			{"20000000-0000-0000-0000-000000000001", "Row A"},
			{"20000000-0000-0000-0000-000000000002", "Row B"},
			{"20000000-0000-0000-0000-000000000003", "Row C"},
		},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != 3 {
		t.Errorf("TotalRowsAffected = %d, want 3", out.TotalRowsAffected)
	}
}

func TestDBExecBatch_Insert_WithReturning(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		ParamSets: [][]any{
			{"20000000-0000-0000-0000-000000000004", "Row D"},
			{"20000000-0000-0000-0000-000000000005", "Row E"},
		},
		Opts: dbExecBatchOpts{Returning: "id, name"},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if len(out.Returning) != 2 {
		t.Fatalf("Returning = %v, want 2 rows", out.Returning)
	}
	if out.Returning[0][1] != "Row D" || out.Returning[1][1] != "Row E" {
		t.Errorf("Returning = %v, want names Row D then Row E in param_sets order", out.Returning)
	}
}

// TestDBExecBatch_ContinueOnError_False_StopsAtFirstFailure_RollsBackAll
// is a regression test for one of the two things that makes exec_batch's
// own transaction sharing correct: without a per-row SAVEPOINT (only used
// when continue_on_error is true), a mid-batch failure must abort and
// roll back the whole batch, including parameter sets that had already
// succeeded earlier in the same call.
func TestDBExecBatch_ContinueOnError_False_StopsAtFirstFailure_RollsBackAll(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		Params: []any{"20000000-0000-0000-0000-000000000006", "Dup"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		ParamSets: [][]any{
			{"20000000-0000-0000-0000-000000000007", "First"},         // succeeds
			{"20000000-0000-0000-0000-000000000008", "Dup"},           // unique violation on name
			{"20000000-0000-0000-0000-000000000009", "Never Reached"}, // must not run
		},
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_error")
	}
	if hostErr.Code != abi.ErrCodeDBBatchError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeDBBatchError)
	}
	if hostErr.Details["index"] != 1 {
		t.Errorf("Details[index] = %v, want 1", hostErr.Details["index"])
	}

	row := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+mc.TenantSlug+".widget WHERE id = $1", "20000000-0000-0000-0000-000000000007")
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 0 {
		t.Errorf("row that succeeded before the failure was not rolled back: count = %d, want 0", count)
	}
}

func TestDBExecBatch_ContinueOnError_True_CommitsSuccessesReportsFailures(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		Params: []any{"2000000a-0000-0000-0000-000000000001", "Dup2"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		ParamSets: [][]any{
			{"2000000a-0000-0000-0000-000000000002", "OK One"},
			{"2000000a-0000-0000-0000-000000000003", "Dup2"}, // fails
			{"2000000a-0000-0000-0000-000000000004", "OK Two"},
		},
		Opts: dbExecBatchOpts{ContinueOnError: true},
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_partial_error")
	}
	if hostErr.Code != abi.ErrCodeDBBatchPartialError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeDBBatchPartialError)
	}
	if hostErr.Details["failed_count"] != 1 {
		t.Errorf("Details[failed_count] = %v, want 1", hostErr.Details["failed_count"])
	}
	if hostErr.Details["total_rows_affected"] != 2 {
		t.Errorf("Details[total_rows_affected] = %v, want 2", hostErr.Details["total_rows_affected"])
	}
	errs, ok := hostErr.Details["errors"].([]batchRowError)
	if !ok || len(errs) != 1 || errs[0].Index != 1 {
		t.Errorf("Details[errors] = %v, want one entry at index 1", hostErr.Details["errors"])
	}
	if errs[0].Code != abi.ErrCodeDBUniqueViolation {
		t.Errorf("errors[0].Code = %q, want %q", errs[0].Code, abi.ErrCodeDBUniqueViolation)
	}
	_ = out // zero value on the error path — see dbExecBatchOutput{} returned alongside a non-nil hostErr

	for _, id := range []string{"2000000a-0000-0000-0000-000000000002", "2000000a-0000-0000-0000-000000000004"} {
		var count int
		if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+mc.TenantSlug+".widget WHERE id = $1", id).Scan(&count); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if count != 1 {
			t.Errorf("successful row %s was not committed: count = %d, want 1", id, count)
		}
	}
}

func TestDBExecBatch_Update_Basic(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	ids := []string{"2000000b-0000-0000-0000-000000000001", "2000000b-0000-0000-0000-000000000002"}
	names := []string{"Before A", "Before B"}
	for i, id := range ids {
		if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, names[i]},
		}); hostErr != nil {
			t.Fatalf("seed insert: %+v", hostErr)
		}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL: "UPDATE widget SET name = $1 WHERE id = $2",
		ParamSets: [][]any{
			{"After A", ids[0]},
			{"After B", ids[1]},
		},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != 2 {
		t.Errorf("TotalRowsAffected = %d, want 2", out.TotalRowsAffected)
	}
}

func TestDBExecBatch_Audit_InsertWritesOneAuditLogRowPerParamSet(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL: "INSERT INTO widget (id, tenant_id, name, secret) VALUES ($1, gen_random_uuid(), $2, $3)",
		ParamSets: [][]any{
			{"2000000c-0000-0000-0000-000000000001", "Audited One", "shh"},
			{"2000000c-0000-0000-0000-000000000002", "Audited Two", "shh"},
		},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 2 {
		t.Fatalf("expected 2 audit_log rows, got %d: %+v", len(rows), rows)
	}
}

func TestDBExecBatch_SkipAudit_NoAuditLogRows(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		ParamSets: [][]any{
			{"2000000d-0000-0000-0000-000000000001", "Unaudited"},
		},
		Opts: dbExecBatchOpts{SkipAudit: true},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 0 {
		t.Fatalf("expected 0 audit_log rows with skip_audit, got %d: %+v", len(rows), rows)
	}
}

func TestDBExecBatch_EtagMismatch_ContinueOnError(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	id := "2000000e-0000-0000-0000-000000000001"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", Params: []any{id, "Etagged"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL: "UPDATE widget SET name = $1 WHERE id = $2 AND etag = $3",
		ParamSets: [][]any{
			{"Renamed", id, "stale-etag"},
		},
		Opts: dbExecBatchOpts{ContinueOnError: true},
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_partial_error")
	}
	errs, ok := hostErr.Details["errors"].([]batchRowError)
	if !ok || len(errs) != 1 || errs[0].Code != abi.ErrCodeDBEtagMismatch {
		t.Errorf("Details[errors] = %v, want one db.etag_mismatch entry", hostErr.Details["errors"])
	}
}

func TestDBExecBatch_BorrowedTransaction_NotAutoCommitted(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	txID := "test-batch-tx"
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, txID)
	// Rolling back an already-committed tx is a safe no-op — this just
	// guarantees the transaction never outlives the test (and blocks the
	// fixture schema's own cleanup) if an assertion below fails first.
	t.Cleanup(func() { _ = tx.Rollback() })

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL: "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		ParamSets: [][]any{
			{"2000000f-0000-0000-0000-000000000001", "In Tx"},
		},
		TxID: txID,
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != 1 {
		t.Errorf("TotalRowsAffected = %d, want 1", out.TotalRowsAffected)
	}

	var count int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+mc.TenantSlug+".widget WHERE id = $1", "2000000f-0000-0000-0000-000000000001").Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 0 {
		t.Errorf("row visible outside the still-open borrowed transaction: count = %d, want 0", count)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestDBExecBatch_UnknownTransactionID(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		ParamSets: [][]any{{"20000010-0000-0000-0000-000000000001", "X"}},
		TxID:      "does-not-exist",
	})
	if hostErr == nil {
		t.Fatal("expected db.transaction_not_found")
	}
	if hostErr.Code != abi.ErrCodeTransactionNotFound {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeTransactionNotFound)
	}
}

func TestDBExecBatch_EmptyParamSets_NoOp(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)",
		ParamSets: nil,
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != 0 {
		t.Errorf("TotalRowsAffected = %d, want 0", out.TotalRowsAffected)
	}
}
