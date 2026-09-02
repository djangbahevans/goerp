package wasm

import (
	"context"
	"fmt"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/abi"
)

// --- Eligibility unit tests: pure, no live Postgres needed (prepareExec
// only parses/validates in memory). ---

func TestResolveCopyPlan_Eligible_UnauditedTable_NoReadbackNeeded(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2)", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	plan := resolveCopyPlan(p, 150, mc)
	if !plan.Eligible {
		t.Fatal("expected eligible: unaudited table, no returning, >100 rows, VALUES all params")
	}
	if plan.Readback {
		t.Error("Readback = true, want false — no audit and no returning requested")
	}
	if len(plan.Columns) != 2 || plan.Columns[0] != "id" || plan.Columns[1] != "name" {
		t.Errorf("Columns = %v, want [id name]", plan.Columns)
	}
}

func TestResolveCopyPlan_Eligible_AuditedTable_PKInColumns(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	plan := resolveCopyPlan(p, 150, mc)
	if !plan.Eligible {
		t.Fatal("expected eligible: audited table, pk (id) present in column list")
	}
	if !plan.Readback {
		t.Error("Readback = false, want true — audited table needs a post-copy read-back for the audit write")
	}
	if plan.PKCol != "id" {
		t.Errorf("PKCol = %q, want %q", plan.PKCol, "id")
	}
}

func TestResolveCopyPlan_Ineligible_AuditedTable_PKNotInColumns(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO widget (tenant_id, name) VALUES ($1, $2)", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: audited table, pk (id) not supplied — no way to correlate copied rows back for the audit write")
	}
}

func TestResolveCopyPlan_Eligible_SkipAudit_PKNotNeeded(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO widget (tenant_id, name) VALUES ($1, $2)", dbExecOpts{SkipAudit: true}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	plan := resolveCopyPlan(p, 150, mc)
	if !plan.Eligible {
		t.Fatal("expected eligible: skip_audit set and no returning requested, so no read-back is needed at all")
	}
	if plan.Readback {
		t.Error("Readback = true, want false")
	}
}

// TestResolveCopyPlan_Eligible_RegardlessOfContinueOnError: resolveCopyPlan
// itself has no opinion on opts.continue_on_error — whether a batch that
// fails partway is safe to retry sequentially is decided by DBExecBatch's
// own dispatch (host_db_exec_batch.go), not here. sdk/go/db.ExecBatch,
// the only Go SDK entry point, always sends continue_on_error: true; an
// exclusion in this function would make the COPY fast path unreachable
// from that SDK entirely.
func TestResolveCopyPlan_Eligible_RegardlessOfContinueOnError(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2)", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); !plan.Eligible {
		t.Fatal("expected eligible: resolveCopyPlan itself has no opinion on continue_on_error")
	}
}

func TestResolveCopyPlan_RowCountThreshold_IsStrictlyGreaterThan100(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2)", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 100, mc); plan.Eligible {
		t.Error("100 rows: expected ineligible — threshold is strictly > 100")
	}
	if plan := resolveCopyPlan(p, 101, mc); !plan.Eligible {
		t.Error("101 rows: expected eligible")
	}
}

func TestResolveCopyPlan_Ineligible_UpdateStatement(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("UPDATE gadget SET name = $1 WHERE id = $2", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Error("expected ineligible: COPY only ever applies to INSERT")
	}
}

// TestResolveCopyPlan_Ineligible_ComputedValueExpression: an INSERT whose
// VALUES list mixes a placeholder with a computed SQL expression
// (gen_random_uuid()) has no way to represent that column's actual value
// in a COPY row — COPY's wire protocol carries literal data, not SQL
// expressions the server would otherwise evaluate per row.
func TestResolveCopyPlan_Ineligible_ComputedValueExpression(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: VALUES mixes a placeholder with a computed expression (gen_random_uuid())")
	}
}

// TestResolveCopyPlan_Ineligible_OnConflict: COPY has no equivalent to ON
// CONFLICT, so an upsert/ignore-duplicate INSERT must never take the
// COPY path — every conflicting row would otherwise surface as a hard
// unique_violation instead of the upsert/ignore behavior the sequential
// path actually provides.
func TestResolveCopyPlan_Ineligible_OnConflict(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET name = excluded.name", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: ON CONFLICT has no COPY equivalent")
	}
}

// TestResolveCopyPlan_Ineligible_OutOfOrderPlaceholders: valid SQL can
// bind placeholders out of column order ("VALUES ($2, $1, $3)" against
// columns (id, tenant_id, name)) — param_sets is indexed by placeholder
// number, not by column position, so accepting this shape into the COPY
// path would silently write each row's values into the wrong columns.
func TestResolveCopyPlan_Ineligible_OutOfOrderPlaceholders(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO widget (id, tenant_id, name) VALUES ($2, $1, $3)", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: placeholders bound out of column order would misalign COPY's row data")
	}
}

// TestResolveCopyPlan_Ineligible_NoExplicitColumnList: an INSERT with no
// explicit column list ("INSERT INTO t VALUES ($1,$2,$3)", valid SQL —
// Postgres infers columns positionally from the table definition) has
// nothing for CopyFrom's own columnNames argument. pgx builds its COPY
// command as "copy tablename (<columnNames>) from stdin binary"; an
// empty columnNames slice produces "copy tablename () from stdin binary"
// — a Postgres syntax error, not merely an unsupported shape.
func TestResolveCopyPlan_Ineligible_NoExplicitColumnList(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget VALUES ($1, $2)", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: no explicit column list — CopyFrom would be called with an empty columnNames slice")
	}
}

func TestPipelineEligible(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	updateP, hostErr := prepareExec("UPDATE gadget SET name = $1 WHERE id = $2", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec update: %+v", hostErr)
	}
	deleteP, hostErr := prepareExec("DELETE FROM gadget WHERE id = $1", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec delete: %+v", hostErr)
	}
	insertP, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2)", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec insert: %+v", hostErr)
	}

	// gadget isn't a declared-audited table (only widget is, per
	// newExecTestDataAuditRegistry), so p.audited is false for every case
	// here and pipelineHasDuplicateAuditTargets never applies — distinct
	// per-row ids are still used so a future audited-table variant of
	// this test can reuse the same shape without collisions.
	updateParamSets := func(n int) [][]any {
		sets := make([][]any, n)
		for i := range n {
			sets[i] = []any{"name", fmt.Sprintf("id-%d", i)}
		}
		return sets
	}
	deleteParamSets := func(n int) [][]any {
		sets := make([][]any, n)
		for i := range n {
			sets[i] = []any{fmt.Sprintf("id-%d", i)}
		}
		return sets
	}

	tests := []struct {
		name      string
		p         preparedExec
		paramSets [][]any
		want      bool
	}{
		{"update multi-row", updateP, updateParamSets(5), true},
		{"delete multi-row", deleteP, deleteParamSets(5), true},
		{"insert never pipeline-eligible", insertP, updateParamSets(5), false},
		{"single row not eligible", updateP, updateParamSets(1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pipelineEligible(tt.p, tt.paramSets); got != tt.want {
				t.Errorf("pipelineEligible() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPipelineEligible_AuditedTable_DuplicateTarget_Ineligible proves the
// duplicate-target guard actually fires — a real gap the sequential
// path's per-row execRow doesn't have (it re-reads immediately before
// each row's own write), but the pipeline path's own up-front pre-read
// would silently produce a stale audit old_data for the second entry
// without this check.
func TestPipelineEligible_AuditedTable_DuplicateTarget_Ineligible(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("UPDATE widget SET name = $1 WHERE id = $2", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}

	dup := [][]any{
		{"First", "same-id"},
		{"Second", "same-id"}, // same target row (WHERE id = $2) as above
	}
	if pipelineEligible(p, dup) {
		t.Fatal("expected ineligible: same row (id = \"same-id\") targeted twice in one audited batch")
	}

	distinct := [][]any{
		{"First", "id-a"},
		{"Second", "id-b"},
	}
	if !pipelineEligible(p, distinct) {
		t.Fatal("expected eligible: distinct target rows")
	}
}

// TestPipelineHasDuplicateAuditTargets_NoWhereClauseParams_AlwaysDuplicate
// covers the case a bare position-count check would miss entirely: a
// WHERE clause with no per-row parameter at all (a constant condition,
// or no WHERE clause) means every row in the batch already targets the
// exact same set of rows — the worst-case version of the staleness this
// check exists to catch, not an absence of anything to check.
func TestPipelineHasDuplicateAuditTargets_NoWhereClauseParams_AlwaysDuplicate(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("UPDATE widget SET name = $1 WHERE tenant_id = '00000000-0000-0000-0000-0000000000f5'", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	paramSets := [][]any{{"First"}, {"Second"}}
	if !pipelineHasDuplicateAuditTargets(p, paramSets) {
		t.Fatal("expected duplicate: the WHERE clause has no per-row parameter, so every row targets the identical set of rows")
	}
}

// TestPipelineEligible_EtagCheckedUpdate_Ineligible: pgx's SendBatch
// flushes every queued statement to Postgres before any result is read
// back, so an etag mismatch — a zero-rows-affected success, not a
// Postgres error — never aborts the transaction the way a real
// constraint violation does. Pipelining an etag-checked UPDATE would let
// every later row in the batch execute regardless of an earlier row's
// reported mismatch, unlike the sequential path's own execRow, which
// stops immediately on the first failure.
func TestPipelineEligible_EtagCheckedUpdate_Ineligible(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("UPDATE widget SET name = $2 WHERE id = $3 AND etag = $1", dbExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	paramSets := [][]any{
		{"v1", "A", "id-a"},
		{"v1", "B", "id-b"},
	}
	if pipelineEligible(p, paramSets) {
		t.Fatal("expected ineligible: an etag-checked UPDATE can't safely pipeline")
	}
}

// --- Integration tests: real Postgres, exercising the fast paths through
// DBExecBatch's own public entry point. ---

const fastPathTenantID = "00000000-0000-0000-0000-0000000000f5"

func TestDBExecBatch_COPYPath_Insert_AuditedTable_WritesAuditAndOrderedReturning(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	const n = 150
	ids := make([]string, n)
	paramSets := make([][]any, n)
	for i := range n {
		id := fmt.Sprintf("30000000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		paramSets[i] = []any{id, fastPathTenantID, fmt.Sprintf("Copy Row %03d", i)}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      dbExecBatchOpts{Returning: "id, name"},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != n {
		t.Errorf("TotalRowsAffected = %d, want %d", out.TotalRowsAffected, n)
	}
	if len(out.Returning) != n {
		t.Fatalf("len(Returning) = %d, want %d", len(out.Returning), n)
	}
	for i, row := range out.Returning {
		if row[0] != ids[i] {
			t.Errorf("Returning[%d][0] = %v, want %q — must come back in param_sets order", i, row[0], ids[i])
		}
		wantName := fmt.Sprintf("Copy Row %03d", i)
		if row[1] != wantName {
			t.Errorf("Returning[%d][1] = %v, want %q", i, row[1], wantName)
		}
	}

	var count int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".widget").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != n {
		t.Errorf("widget row count = %d, want %d", count, n)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != n {
		t.Fatalf("audit_log rows = %d, want %d", len(rows), n)
	}
}

func TestDBExecBatch_COPYPath_Insert_SkipAudit_UnauditedShapeStillWorks(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	const n = 120
	paramSets := make([][]any, n)
	for i := range n {
		paramSets[i] = []any{fmt.Sprintf("30100000-0000-0000-0000-%012d", i+1), fastPathTenantID, fmt.Sprintf("Skip Audit %03d", i)}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      dbExecBatchOpts{SkipAudit: true},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != n {
		t.Errorf("TotalRowsAffected = %d, want %d", out.TotalRowsAffected, n)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	if len(rows) != 0 {
		t.Fatalf("audit_log rows = %d, want 0 with skip_audit", len(rows))
	}
}

// TestDBExecBatch_Insert_NoExplicitColumnList_LargeBatch_StillSucceeds is
// the end-to-end regression test for a real bug: before
// resolveCopyPlan's own column-list check, a >100-row INSERT with no
// explicit column list (valid SQL, worked via the sequential path
// before this ticket) would be marked COPY-eligible with an empty
// column list, and execBatchCopy's CopyFrom call would fail outright
// with a Postgres syntax error — a previously-working call newly
// hard-failing 100% of the time past the COPY threshold, with no
// retry since the default opts.continue_on_error is false.
func TestDBExecBatch_Insert_NoExplicitColumnList_LargeBatch_StillSucceeds(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	const n = 120
	paramSets := make([][]any, n)
	for i := range n {
		paramSets[i] = []any{fmt.Sprintf("30b00000-0000-0000-0000-%012d", i+1), fmt.Sprintf("No Cols %03d", i)}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "INSERT INTO gadget VALUES ($1, $2)",
		ParamSets: paramSets,
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != n {
		t.Errorf("TotalRowsAffected = %d, want %d", out.TotalRowsAffected, n)
	}

	var count int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".gadget").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != n {
		t.Errorf("gadget row count = %d, want %d", count, n)
	}
}

// TestDBExecBatch_COPYPath_UniqueViolation_FailsWholeBatchAtomically
// proves a real mid-COPY failure surfaces as db.batch_error and leaves no
// partially-copied rows — a genuine constraint violation against real
// Postgres, not a simulated one.
func TestDBExecBatch_COPYPath_UniqueViolation_FailsWholeBatchAtomically(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	dupName := "Duplicate Name"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		Params: []any{"30200000-0000-0000-0000-000000000001", fastPathTenantID, dupName},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	const n = 110
	paramSets := make([][]any, n)
	for i := range n {
		name := fmt.Sprintf("COPY Unique %03d", i)
		if i == n/2 {
			name = dupName // collides with the seeded row's own unique name
		}
		paramSets[i] = []any{fmt.Sprintf("30200000-0000-0000-0000-%012d", i+2), fastPathTenantID, name}
	}

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_error from a real unique violation mid-COPY")
	}
	if hostErr.Code != abi.ErrCodeDBBatchError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeDBBatchError)
	}

	var count int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".widget").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("widget row count = %d, want 1 (only the seeded row) — COPY failure must not leave partially-copied rows", count)
	}
}

// TestDBExecBatch_COPYPath_ContinueOnErrorTrue_PartialFailure_RetriesSequentially
// proves the end-to-end chain sdk/go/db.ExecBatch depends on: it — the
// only Go SDK entry point — always sends continue_on_error: true, while
// resolveCopyPlan/pipelineEligible take no opinion on that option at
// all, leaving DBExecBatch's own dispatch as the sole place its
// consequences are handled. A COPY-eligible-shaped batch (>100 rows, no
// tx_id) with one failing row must still produce the sequential path's
// own db.batch_partial_error — real per-row index attribution and
// partial commits — not a bare db.batch_error the fast path alone could
// produce.
func TestDBExecBatch_COPYPath_ContinueOnErrorTrue_PartialFailure_RetriesSequentially(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	dupName := "CoE Duplicate"
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		Params: []any{"30900000-0000-0000-0000-000000000001", fastPathTenantID, dupName},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	const n = 110
	const failIndex = 40
	paramSets := make([][]any, n)
	for i := range n {
		name := fmt.Sprintf("CoE Row %03d", i)
		if i == failIndex {
			name = dupName // collides with the seeded row's own unique name
		}
		paramSets[i] = []any{fmt.Sprintf("30900000-0000-0000-0000-%012d", i+2), fastPathTenantID, name}
	}

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      dbExecBatchOpts{ContinueOnError: true},
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_partial_error")
	}
	if hostErr.Code != abi.ErrCodeDBBatchPartialError {
		t.Fatalf("Code = %q, want %q — continue_on_error: true must retry via the sequential path on a fast-path failure, not return the fast path's own db.batch_error", hostErr.Code, abi.ErrCodeDBBatchPartialError)
	}
	if hostErr.Details["failed_count"] != 1 {
		t.Errorf("Details[failed_count] = %v, want 1", hostErr.Details["failed_count"])
	}
	errs, ok := hostErr.Details["errors"].([]batchRowError)
	if !ok || len(errs) != 1 || errs[0].Index != failIndex {
		t.Errorf("Details[errors] = %v, want one entry at index %d", hostErr.Details["errors"], failIndex)
	}

	var count int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".widget").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != n {
		t.Errorf("widget row count = %d, want %d (the seed row plus every successful row — only the one duplicate should fail)", count, n)
	}
}

// TestDBExecBatch_COPYPath_ContinueOnErrorTrue_BorrowedTx_NeverRetries_ReportsRealIndex
// proves the fast path is skipped entirely (not attempted-then-retried)
// when continue_on_error: true is combined with a caller-supplied tx_id:
// a failed fast attempt has no clean way to be undone without touching
// the caller's own transaction, so this shape must go straight to the
// sequential path — evidenced by a real per-row index in the failure,
// never the fast path's own -1 sentinel.
func TestDBExecBatch_COPYPath_ContinueOnErrorTrue_BorrowedTx_NeverRetries_ReportsRealIndex(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	txID := "test-batch-coe-borrowed-tx"
	tx := registerTenantScopedTestTx(t, ctx, primaryDB, mc, txID)
	t.Cleanup(func() { _ = tx.Rollback() })

	dupName := "Borrowed CoE Duplicate"
	if _, err := tx.ExecContext(ctx, "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		"30a00000-0000-0000-0000-000000000001", fastPathTenantID, dupName); err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	const n = 110
	const failIndex = 5
	paramSets := make([][]any, n)
	for i := range n {
		name := fmt.Sprintf("Borrowed CoE Row %03d", i)
		if i == failIndex {
			name = dupName
		}
		paramSets[i] = []any{fmt.Sprintf("30a00000-0000-0000-0000-%012d", i+2), fastPathTenantID, name}
	}

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      dbExecBatchOpts{ContinueOnError: true},
		TxID:      txID,
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_partial_error")
	}
	if hostErr.Code != abi.ErrCodeDBBatchPartialError {
		t.Fatalf("Code = %q, want %q", hostErr.Code, abi.ErrCodeDBBatchPartialError)
	}
	errs, ok := hostErr.Details["errors"].([]batchRowError)
	if !ok || len(errs) != 1 || errs[0].Index != failIndex {
		t.Errorf("Details[errors] = %v, want one entry at index %d — a real index means the sequential path ran, never the fast path's own -1 sentinel", hostErr.Details["errors"], failIndex)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var count int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".widget").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != n {
		t.Errorf("widget row count = %d, want %d", count, n)
	}
}

// TestDBExecBatch_COPYPath_ReadbackChunking_CrossesChunkBoundary: binding
// every row's own primary-key value in one SELECT would exceed
// Postgres's 65535-bound-parameters-per-statement limit for a
// sufficiently large batch — a batch the COPY step itself has no such
// limit for, so copyReadback chunks its own read-back instead. n here
// spans exactly two read-back chunks (maxReadbackChunkParams + 50),
// proving both chunks' results still come back in param_sets order once
// concatenated.
func TestDBExecBatch_COPYPath_ReadbackChunking_CrossesChunkBoundary(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	n := maxReadbackChunkParams + 50
	ids := make([]string, n)
	paramSets := make([][]any, n)
	for i := range n {
		id := fmt.Sprintf("30800000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		paramSets[i] = []any{id, fastPathTenantID, fmt.Sprintf("Chunk Row %d", i)}
	}

	// skip_audit keeps this test's own runtime down (no audit_log writes)
	// while opts.returning still forces the exact read-back path being
	// tested — readbackNeeded is true here purely from requestedCols.
	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      dbExecBatchOpts{Returning: "id", SkipAudit: true},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != n {
		t.Errorf("TotalRowsAffected = %d, want %d", out.TotalRowsAffected, n)
	}
	if len(out.Returning) != n {
		t.Fatalf("len(Returning) = %d, want %d", len(out.Returning), n)
	}
	for i, row := range out.Returning {
		if row[0] != ids[i] {
			t.Fatalf("Returning[%d][0] = %v, want %q — chunk boundary must not disturb param_sets ordering", i, row[0], ids[i])
		}
	}
}

func TestDBExecBatch_PipelinePath_Update_AuditedTable_WritesAuditAndReturning(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	const n = 10
	ids := make([]string, n)
	paramSets := make([][]any, n)
	for i := range n {
		id := fmt.Sprintf("30300000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
			Params: []any{id, fastPathTenantID, fmt.Sprintf("Before %03d", i)},
		}); hostErr != nil {
			t.Fatalf("seed insert %d: %+v", i, hostErr)
		}
		paramSets[i] = []any{fmt.Sprintf("After %03d", i), id}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "UPDATE widget SET name = $1 WHERE id = $2",
		ParamSets: paramSets,
		Opts:      dbExecBatchOpts{Returning: "id, name"},
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != n {
		t.Errorf("TotalRowsAffected = %d, want %d", out.TotalRowsAffected, n)
	}
	if len(out.Returning) != n {
		t.Fatalf("len(Returning) = %d, want %d", len(out.Returning), n)
	}
	for i, row := range out.Returning {
		if row[0] != ids[i] {
			t.Errorf("Returning[%d][0] = %v, want %q — must come back in param_sets order", i, row[0], ids[i])
		}
		wantName := fmt.Sprintf("After %03d", i)
		if row[1] != wantName {
			t.Errorf("Returning[%d][1] = %v, want %q", i, row[1], wantName)
		}
	}

	// n INSERT rows from the seed loop above, plus n UPDATE rows from the
	// pipelined batch itself.
	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	var updateRows int
	for _, r := range rows {
		if r.Operation == "UPDATE" {
			updateRows++
		}
	}
	if updateRows != n {
		t.Fatalf("UPDATE audit_log rows = %d, want %d (out of %d total)", updateRows, n, len(rows))
	}
}

func TestDBExecBatch_PipelinePath_Delete_RemovesRows(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := context.Background()

	const n = 8
	paramSets := make([][]any, n)
	for i := range n {
		id := fmt.Sprintf("30400000-0000-0000-0000-%012d", i+1)
		if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
			Params: []any{id, fastPathTenantID, fmt.Sprintf("To Delete %03d", i)},
		}); hostErr != nil {
			t.Fatalf("seed insert %d: %+v", i, hostErr)
		}
		paramSets[i] = []any{id}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "DELETE FROM widget WHERE id = $1",
		ParamSets: paramSets,
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != n {
		t.Errorf("TotalRowsAffected = %d, want %d", out.TotalRowsAffected, n)
	}

	var count int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".widget").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("widget row count = %d, want 0", count)
	}
}

// TestDBExecBatch_PipelinePath_EtagMismatch_ReportsAsBatchError proves the
// pipeline path's own etag-mismatch detection matches the sequential
// path's real Postgres behavior — a stale etag reported as
// db.etag_mismatch, not a bare zero-rows-affected success.
// TestDBExecBatch_EtagCheckedUpdateBatch_UsesSequentialPath_ReportsMismatch
// is the end-to-end counterpart to pipelineEligible's own
// hadEtagCheck-excludes-pipelining rule: an otherwise pipeline-shaped
// UPDATE batch (multiple rows, no continue_on_error) whose WHERE clause
// checks etag must still take the sequential path — proving the
// exclusion actually reaches DBExecBatch's own dispatch, not just the
// eligibility function in isolation — and that path's real per-row
// SAVEPOINT handling correctly stops at the first mismatch and reports
// it, exactly as it would for any other sequential-path failure.
func TestDBExecBatch_EtagCheckedUpdateBatch_UsesSequentialPath_ReportsMismatch(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	const n = 5
	ids := make([]string, n)
	for i := range n {
		id := fmt.Sprintf("30500000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL:    "INSERT INTO widget (id, tenant_id, name, etag) VALUES ($1, $2, $3, 'v1')",
			Params: []any{id, fastPathTenantID, fmt.Sprintf("Etag %03d", i)},
		}); hostErr != nil {
			t.Fatalf("seed insert %d: %+v", i, hostErr)
		}
	}

	paramSets := make([][]any, n)
	for i, id := range ids {
		paramSets[i] = []any{"stale-etag", id} // every row's own real etag is "v1", not "stale-etag"
	}

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "UPDATE widget SET name = 'Changed' WHERE id = $2 AND etag = $1",
		ParamSets: paramSets,
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_error from an etag mismatch on the first row")
	}
	if hostErr.Code != abi.ErrCodeDBBatchError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abi.ErrCodeDBBatchError)
	}
	if hostErr.Details["code"] != abi.ErrCodeDBEtagMismatch {
		t.Errorf("Details[code] = %v, want %q", hostErr.Details["code"], abi.ErrCodeDBEtagMismatch)
	}
}

func TestDBExecBatch_PipelinePath_ContinueOnError_FallsBackToSequential(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := context.Background()

	const n = 5
	ids := make([]string, n)
	for i := range n {
		id := fmt.Sprintf("30600000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
			SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
			Params: []any{id, fastPathTenantID, fmt.Sprintf("CoE %03d", i)},
		}); hostErr != nil {
			t.Fatalf("seed insert %d: %+v", i, hostErr)
		}
	}
	// Give one row a name that will collide on the unique constraint once
	// updated, so continue_on_error has a real per-row failure to report —
	// only possible via the sequential path's own per-row SAVEPOINT, not
	// the pipeline fast path this test's own eligible-shaped batch would
	// otherwise take.
	if _, hostErr := DBExec(ctx, primaryDB, mc, dbExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		Params: []any{"30600000-0000-0000-0000-000000000099", fastPathTenantID, "Taken"},
	}); hostErr != nil {
		t.Fatalf("seed conflicting name: %+v", hostErr)
	}

	paramSets := make([][]any, n)
	for i, id := range ids {
		name := fmt.Sprintf("CoE After %03d", i)
		if i == 2 {
			name = "Taken" // collides with the seeded row above
		}
		paramSets[i] = []any{name, id}
	}

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, dbExecBatchInput{
		SQL:       "UPDATE widget SET name = $1 WHERE id = $2",
		ParamSets: paramSets,
		Opts:      dbExecBatchOpts{ContinueOnError: true},
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_partial_error")
	}
	if hostErr.Code != abi.ErrCodeDBBatchPartialError {
		t.Errorf("Code = %q, want %q — continue_on_error must use the sequential path's own per-row partial-failure reporting, not the pipeline fast path", hostErr.Code, abi.ErrCodeDBBatchPartialError)
	}
	if hostErr.Details["failed_count"] != 1 {
		t.Errorf("Details[failed_count] = %v, want 1", hostErr.Details["failed_count"])
	}
}
