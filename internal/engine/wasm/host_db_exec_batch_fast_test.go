package wasm

import (
	"encoding/json/v2"
	"fmt"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestResolveCopyPlan_Eligible_UnauditedTable_NoReadbackNeeded(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2)", abiv1.DBExecOpts{}, mc)
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
	p, hostErr := prepareExec("INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)", abiv1.DBExecOpts{}, mc)
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
	p, hostErr := prepareExec("INSERT INTO widget (tenant_id, name) VALUES ($1, $2)", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: audited table, pk (id) not supplied — no way to correlate copied rows back for the audit write")
	}
}

func TestResolveCopyPlan_Eligible_SkipAudit_PKNotNeeded(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO widget (tenant_id, name) VALUES ($1, $2)", abiv1.DBExecOpts{SkipAudit: true}, mc)
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

// continue_on_error is handled by dispatch rather than COPY eligibility; SDK batches
// always request it.
func TestResolveCopyPlan_Eligible_RegardlessOfContinueOnError(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2)", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); !plan.Eligible {
		t.Fatal("expected eligible: resolveCopyPlan itself has no opinion on continue_on_error")
	}
}

func TestResolveCopyPlan_RowCountThreshold_IsStrictlyGreaterThan100(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2)", abiv1.DBExecOpts{}, mc)
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
	p, hostErr := prepareExec("UPDATE gadget SET name = $1 WHERE id = $2", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Error("expected ineligible: COPY only ever applies to INSERT")
	}
}

// COPY carries literal values and cannot evaluate a SQL expression for an inserted column.
func TestResolveCopyPlan_Ineligible_ComputedValueExpression(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO widget (id, tenant_id, name) VALUES ($1, gen_random_uuid(), $2)", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: VALUES mixes a placeholder with a computed expression (gen_random_uuid())")
	}
}

// COPY cannot preserve ON CONFLICT semantics, so those inserts require sequential
// execution.
func TestResolveCopyPlan_Ineligible_OnConflict(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET name = excluded.name", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: ON CONFLICT has no COPY equivalent")
	}
}

// COPY values follow column order; accepting placeholders in a different order would
// silently write to the wrong columns.
func TestResolveCopyPlan_Ineligible_OutOfOrderPlaceholders(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO widget (id, tenant_id, name) VALUES ($2, $1, $3)", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: placeholders bound out of column order would misalign COPY's row data")
	}
}

// Without explicit columns, pgx would generate an invalid empty COPY column list.
func TestResolveCopyPlan_Ineligible_NoExplicitColumnList(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("INSERT INTO gadget VALUES ($1, $2)", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	if plan := resolveCopyPlan(p, 150, mc); plan.Eligible {
		t.Fatal("expected ineligible: no explicit column list — CopyFrom would be called with an empty columnNames slice")
	}
}

func TestPipelineEligible(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	updateP, hostErr := prepareExec("UPDATE gadget SET name = $1 WHERE id = $2", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec update: %+v", hostErr)
	}
	deleteP, hostErr := prepareExec("DELETE FROM gadget WHERE id = $1", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec delete: %+v", hostErr)
	}
	insertP, hostErr := prepareExec("INSERT INTO gadget (id, name) VALUES ($1, $2)", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec insert: %+v", hostErr)
	}

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

// Audited repeated targets need sequential pre-reads so each old_data reflects the
// preceding write.
func TestPipelineEligible_AuditedTable_DuplicateTarget_Ineligible(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("UPDATE widget SET name = $1 WHERE id = $2", abiv1.DBExecOpts{}, mc)
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

// A constant or absent WHERE predicate repeats every target set, even without per-row
// parameters.
func TestPipelineHasDuplicateAuditTargets_NoWhereClauseParams_AlwaysDuplicate(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("UPDATE widget SET name = $1 WHERE tenant_id = '00000000-0000-0000-0000-0000000000f5'", abiv1.DBExecOpts{}, mc)
	if hostErr != nil {
		t.Fatalf("prepareExec: %+v", hostErr)
	}
	paramSets := [][]any{{"First"}, {"Second"}}
	if !pipelineHasDuplicateAuditTargets(p, paramSets) {
		t.Fatal("expected duplicate: the WHERE clause has no per-row parameter, so every row targets the identical set of rows")
	}
}

// An etag mismatch is a successful zero-row update, so Postgres does not abort later
// queued writes. Such updates must avoid pipelining.
func TestPipelineEligible_EtagCheckedUpdate_Ineligible(t *testing.T) {
	mc := newExecTestModuleContext("acme")
	p, hostErr := prepareExec("UPDATE widget SET name = $2 WHERE id = $3 AND etag = $1", abiv1.DBExecOpts{}, mc)
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

const fastPathTenantID = "00000000-0000-0000-0000-0000000000f5"

func TestDBExecBatch_COPYPath_Insert_AuditedTable_WritesAuditAndOrderedReturning(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := t.Context()

	const n = 150
	ids := make([]string, n)
	paramSets := make([][]any, n)
	for i := range n {
		id := fmt.Sprintf("30000000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		paramSets[i] = []any{id, fastPathTenantID, fmt.Sprintf("Copy Row %03d", i)}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      abiv1.DBExecBatchOpts{Returning: "id, name"},
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
	ctx := t.Context()

	const n = 120
	paramSets := make([][]any, n)
	for i := range n {
		paramSets[i] = []any{fmt.Sprintf("30100000-0000-0000-0000-%012d", i+1), fastPathTenantID, fmt.Sprintf("Skip Audit %03d", i)}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      abiv1.DBExecBatchOpts{SkipAudit: true},
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

func TestDBExecBatch_Insert_NoExplicitColumnList_LargeBatch_StillSucceeds(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := t.Context()

	const n = 120
	paramSets := make([][]any, n)
	for i := range n {
		paramSets[i] = []any{fmt.Sprintf("30b00000-0000-0000-0000-%012d", i+1), fmt.Sprintf("No Cols %03d", i)}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
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

func TestDBExecBatch_COPYPath_UniqueViolation_FailsWholeBatchAtomically(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := t.Context()

	dupName := "Duplicate Name"
	if _, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
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

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_error from a real unique violation mid-COPY")
	}
	if hostErr.Code != abiv1.ErrCodeDBBatchError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abiv1.ErrCodeDBBatchError)
	}

	var count int
	if err := primaryDB.QueryRowContext(ctx, "SELECT count(*) FROM tenant_"+slug+".widget").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("widget row count = %d, want 1 (only the seeded row) — COPY failure must not leave partially-copied rows", count)
	}
}

// An owned COPY transaction can roll back and retry sequentially to attribute partial
// failures to their row indexes.
func TestDBExecBatch_COPYPath_ContinueOnErrorTrue_PartialFailure_RetriesSequentially(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := t.Context()

	dupName := "CoE Duplicate"
	if _, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
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

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      abiv1.DBExecBatchOpts{ContinueOnError: true},
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_partial_error")
	}
	if hostErr.Code != abiv1.ErrCodeDBBatchPartialError {
		t.Fatalf("Code = %q, want %q — continue_on_error: true must retry via the sequential path on a fast-path failure, not return the fast path's own db.batch_error", hostErr.Code, abiv1.ErrCodeDBBatchPartialError)
	}
	if hostErr.Details["failed_count"] != 1 {
		t.Errorf("Details[failed_count] = %v, want 1", hostErr.Details["failed_count"])
	}
	errs, ok := hostErr.Details["errors"].([]abiv1.DBBatchRowError)
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

// A borrowed transaction cannot undo a failed fast attempt independently, so
// continue_on_error uses the sequential path directly.
func TestDBExecBatch_COPYPath_ContinueOnErrorTrue_BorrowedTx_NeverRetries_ReportsRealIndex(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := t.Context()

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

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      abiv1.DBExecBatchOpts{ContinueOnError: true},
		TxID:      txID,
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_partial_error")
	}
	if hostErr.Code != abiv1.ErrCodeDBBatchPartialError {
		t.Fatalf("Code = %q, want %q", hostErr.Code, abiv1.ErrCodeDBBatchPartialError)
	}
	errs, ok := hostErr.Details["errors"].([]abiv1.DBBatchRowError)
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

// COPY read-back must chunk primary-key parameters below Postgres's limit while retaining
// input order.
func TestDBExecBatch_COPYPath_ReadbackChunking_CrossesChunkBoundary(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := t.Context()

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
	out, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
		ParamSets: paramSets,
		Opts:      abiv1.DBExecBatchOpts{Returning: "id", SkipAudit: true},
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

// Different predicates can match one physical row. Audit pairing must preserve a separate
// entry per statement instead of collapsing entries by primary key.
func TestDBExecBatch_PipelinePath_Update_AuditedTable_OverlappingTargets_BothEntriesPreserved(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := t.Context()

	id := "30f00000-0000-0000-0000-000000000001"
	if _, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
		SQL:    "INSERT INTO widget (id, tenant_id, name, secret) VALUES ($1, $2, $3, $4)",
		Params: []any{id, fastPathTenantID, "Before", "mysecret"},
	}); hostErr != nil {
		t.Fatalf("seed insert: %+v", hostErr)
	}

	paramSets := [][]any{
		{"After-0", id, "no-such-secret-0"},                             // matches via id
		{"After-1", "00000000-0000-0000-0000-000000000000", "mysecret"}, // matches via secret — same row, different bound values
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "UPDATE widget SET name = $1 WHERE id = $2 OR secret = $3",
		ParamSets: paramSets,
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != 2 {
		t.Errorf("TotalRowsAffected = %d, want 2 (both statements affect the same physical row)", out.TotalRowsAffected)
	}

	var finalName string
	if err := primaryDB.QueryRowContext(ctx, "SELECT name FROM tenant_"+slug+".widget WHERE id = $1", id).Scan(&finalName); err != nil {
		t.Fatalf("query final name: %v", err)
	}
	if finalName != "After-1" {
		t.Errorf("final name = %q, want %q (second statement runs after the first in the same pipelined batch)", finalName, "After-1")
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	var updateRows []auditLogRow
	for _, r := range rows {
		if r.Operation == "UPDATE" {
			updateRows = append(updateRows, r)
		}
	}
	if len(updateRows) != 2 {
		t.Fatalf("UPDATE audit_log rows = %d, want 2 — one per statement, not collapsed by primary-key pairing", len(updateRows))
	}

	gotNames := make(map[string]bool, 2)
	for _, r := range updateRows {
		var newData map[string]any
		if err := json.Unmarshal([]byte(r.NewData.String), &newData); err != nil {
			t.Fatalf("unmarshal new_data: %v", err)
		}
		gotNames[fmt.Sprint(newData["name"])] = true
	}
	for _, want := range []string{"After-0", "After-1"} {
		if !gotNames[want] {
			t.Errorf("no audit_log entry with new_data.name = %q — got names %v", want, gotNames)
		}
	}
}

func TestDBExecBatch_PipelinePath_Update_AuditedTable_WritesAuditAndReturning(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := t.Context()

	const n = 10
	ids := make([]string, n)
	paramSets := make([][]any, n)
	for i := range n {
		id := fmt.Sprintf("30300000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		if _, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
			SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
			Params: []any{id, fastPathTenantID, fmt.Sprintf("Before %03d", i)},
		}); hostErr != nil {
			t.Fatalf("seed insert %d: %+v", i, hostErr)
		}
		paramSets[i] = []any{fmt.Sprintf("After %03d", i), id}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "UPDATE widget SET name = $1 WHERE id = $2",
		ParamSets: paramSets,
		Opts:      abiv1.DBExecBatchOpts{Returning: "id, name"},
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

// Check rows across the audit insertion chunk boundary to expose attribution and off-by-
// one errors.
func TestDBExecBatch_PipelinePath_Update_AuditedTable_CrossesAuditChunkBoundary(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := t.Context()

	const n = 700
	ids := make([]string, n)
	paramSets := make([][]any, n)
	for i := range n {
		id := fmt.Sprintf("30700000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		if _, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
			SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
			Params: []any{id, fastPathTenantID, fmt.Sprintf("Chunk Before %04d", i)},
		}); hostErr != nil {
			t.Fatalf("seed insert %d: %+v", i, hostErr)
		}
		paramSets[i] = []any{fmt.Sprintf("Chunk After %04d", i), id}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "UPDATE widget SET name = $1 WHERE id = $2",
		ParamSets: paramSets,
	})
	if hostErr != nil {
		t.Fatalf("DBExecBatch: %+v", hostErr)
	}
	if out.TotalRowsAffected != n {
		t.Errorf("TotalRowsAffected = %d, want %d", out.TotalRowsAffected, n)
	}

	rows := queryAuditLogRows(t, primaryDB, slug, "widget")
	byRecordID := make(map[string]auditLogRow, len(rows))
	var updateRows int
	for _, r := range rows {
		if r.Operation == "UPDATE" {
			updateRows++
			byRecordID[r.RecordID] = r
		}
	}
	if updateRows != n {
		t.Fatalf("UPDATE audit_log rows = %d, want %d", updateRows, n)
	}

	for _, i := range []int{0, 624, 625, 626, n - 1} {
		row, ok := byRecordID[ids[i]]
		if !ok {
			t.Fatalf("no audit_log UPDATE row for id %s (index %d)", ids[i], i)
		}
		var oldData, newData map[string]any
		if err := json.Unmarshal([]byte(row.OldData.String), &oldData); err != nil {
			t.Fatalf("unmarshal old_data for index %d: %v", i, err)
		}
		if err := json.Unmarshal([]byte(row.NewData.String), &newData); err != nil {
			t.Fatalf("unmarshal new_data for index %d: %v", i, err)
		}
		wantOld := fmt.Sprintf("Chunk Before %04d", i)
		wantNew := fmt.Sprintf("Chunk After %04d", i)
		if oldData["name"] != wantOld {
			t.Errorf("index %d: old_data[name] = %v, want %q", i, oldData["name"], wantOld)
		}
		if newData["name"] != wantNew {
			t.Errorf("index %d: new_data[name] = %v, want %q", i, newData["name"], wantNew)
		}
	}
}

func TestDBExecBatch_PipelinePath_Delete_RemovesRows(t *testing.T) {
	primaryDB, slug, mc := setupExecTest(t)
	ctx := t.Context()

	const n = 8
	paramSets := make([][]any, n)
	for i := range n {
		id := fmt.Sprintf("30400000-0000-0000-0000-%012d", i+1)
		if _, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
			SQL:    "INSERT INTO widget (id, tenant_id, name) VALUES ($1, $2, $3)",
			Params: []any{id, fastPathTenantID, fmt.Sprintf("To Delete %03d", i)},
		}); hostErr != nil {
			t.Fatalf("seed insert %d: %+v", i, hostErr)
		}
		paramSets[i] = []any{id}
	}

	out, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
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

func TestDBExecBatch_EtagCheckedUpdateBatch_UsesSequentialPath_ReportsMismatch(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := t.Context()

	const n = 5
	ids := make([]string, n)
	for i := range n {
		id := fmt.Sprintf("30500000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		if _, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
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

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "UPDATE widget SET name = 'Changed' WHERE id = $2 AND etag = $1",
		ParamSets: paramSets,
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_error from an etag mismatch on the first row")
	}
	if hostErr.Code != abiv1.ErrCodeDBBatchError {
		t.Errorf("Code = %q, want %q", hostErr.Code, abiv1.ErrCodeDBBatchError)
	}
	if hostErr.Details["code"] != abiv1.ErrCodeDBEtagMismatch {
		t.Errorf("Details[code] = %v, want %q", hostErr.Details["code"], abiv1.ErrCodeDBEtagMismatch)
	}
}

func TestDBExecBatch_PipelinePath_ContinueOnError_FallsBackToSequential(t *testing.T) {
	primaryDB, _, mc := setupExecTest(t)
	ctx := t.Context()

	const n = 5
	ids := make([]string, n)
	for i := range n {
		id := fmt.Sprintf("30600000-0000-0000-0000-%012d", i+1)
		ids[i] = id
		if _, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
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
	if _, hostErr := DBExec(ctx, primaryDB, mc, abiv1.DBExecInput{
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

	_, hostErr := DBExecBatch(ctx, primaryDB, mc, abiv1.DBExecBatchInput{
		SQL:       "UPDATE widget SET name = $1 WHERE id = $2",
		ParamSets: paramSets,
		Opts:      abiv1.DBExecBatchOpts{ContinueOnError: true},
	})
	if hostErr == nil {
		t.Fatal("expected db.batch_partial_error")
	}
	if hostErr.Code != abiv1.ErrCodeDBBatchPartialError {
		t.Errorf("Code = %q, want %q — continue_on_error must use the sequential path's own per-row partial-failure reporting, not the pipeline fast path", hostErr.Code, abiv1.ErrCodeDBBatchPartialError)
	}
	if hostErr.Details["failed_count"] != 1 {
		t.Errorf("Details[failed_count] = %v, want 1", hostErr.Details["failed_count"])
	}
}
