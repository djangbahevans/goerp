package wasm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const copyEligibleRowThreshold = 100

// copyPlan is resolveCopyPlan's own verdict — either the batch isn't
// COPY-eligible at all (Eligible == false, every other field zero), or it
// is, with everything execBatchCopy needs to run it.
type copyPlan struct {
	Eligible bool
	Columns  []string // the INSERT's own column list, positionally aligned with each ParamSet
	PKCol    string   // only meaningful when Readback is true
	Readback bool     // whether a post-COPY SELECT is needed at all
}

// resolveCopyPlan requires caller-supplied primary keys when audit or RETURNING needs
// read-back because COPY has no RETURNING clause. continue_on_error is handled by retrying
// safe failures sequentially.
func resolveCopyPlan(p preparedExec, numRows int, modCtx *ModuleContext) copyPlan {
	if p.stmt.Operation != "INSERT" || numRows <= copyEligibleRowThreshold {
		return copyPlan{}
	}
	if !insertValuesAllParams(p.stmt) {
		return copyPlan{}
	}
	columns := insertColumnNames(p.stmt)

	// p.audited is only ever true when opts.SkipAudit was already false —
	// prepareExec (host_db_exec.go) only calls resolveAuditedExecTable
	// under "if !opts.SkipAudit" — so checking p.audited alone already
	// implies !opts.SkipAudit.
	readbackNeeded := p.audited || p.requestedCols != nil
	if !readbackNeeded {
		return copyPlan{Eligible: true, Columns: columns}
	}

	pkCol := p.pkCol
	if pkCol == "" {
		pkCol, _ = insertPrimaryKeyColumn(modCtx, p.table)
	}
	if pkCol == "" || !slices.Contains(columns, pkCol) {
		return copyPlan{}
	}
	return copyPlan{Eligible: true, Columns: columns, PKCol: pkCol, Readback: true}
}

// pipelineEligible excludes etag-checked updates because all statements execute before
// results are read, allowing later writes after a zero-row mismatch. It also excludes
// singleton batches and repeated audited targets.
func pipelineEligible(p preparedExec, paramSets [][]any) bool {
	if (p.stmt.Operation != "UPDATE" && p.stmt.Operation != "DELETE") || len(paramSets) <= 1 {
		return false
	}
	if p.hadEtagCheck {
		return false
	}
	// The pipeline path has no change-entry pass; the sequential path's
	// execRow writes them.
	if p.tracked {
		return false
	}
	// The batched audit pre-read (captureRowsBeforeExecBatch) selects the
	// target row as a bare composite and can't carry FROM/USING items.
	if p.audited && len(p.stmt.FromClause) > 0 {
		return false
	}
	if p.audited && pipelineHasDuplicateAuditTargets(p, paramSets) {
		return false
	}
	return true
}

// pipelineHasDuplicateAuditTargets excludes repeated targets on audited tables because
// pre-reading the entire batch would record stale old_data for later writes to the same
// row.
func pipelineHasDuplicateAuditTargets(p preparedExec, paramSets [][]any) bool {
	paramNums := whereClauseParamNumbers(p.stmt.WhereClause)
	if len(paramNums) == 0 {
		// No per-row parameter in the WHERE clause at all (a constant
		// condition, or no WHERE clause) — every row in the batch already
		// targets the exact same set of rows, the worst case this check
		// exists to catch. pipelineEligible only calls this with
		// len(paramSets) > 1, so reaching here always means a duplicate
		// target.
		return true
	}
	seen := make(map[string]bool, len(paramSets))
	var key strings.Builder
	for _, params := range paramSets {
		key.Reset()
		for _, n := range paramNums {
			// A malformed row (fewer values than the WHERE clause's own
			// highest $n) is a caller bug this function shouldn't panic
			// on — the sequential path's own execRow surfaces that same
			// malformed input as a normal Postgres/HostError instead, so
			// treat it here as "can't determine, be conservative" rather
			// than indexing out of bounds.
			if int(n) > len(params) {
				return true
			}
			fmt.Fprintf(&key, "%#v|", params[n-1])
		}
		k := key.String()
		if seen[k] {
			return true
		}
		seen[k] = true
	}
	return false
}

// whereClauseParamNumbers returns the $n parameter numbers whereClause
// itself references — the params that determine which row(s) a
// statement targets, independent of whatever an UPDATE's own SET clause
// separately assigns.
func whereClauseParamNumbers(whereClause *pg_query.Node) []int32 {
	if whereClause == nil {
		return nil
	}
	var nums []int32
	walkPGQueryTree(whereClause.ProtoReflect(), func(m protoreflect.Message) bool {
		if pr, ok := m.Interface().(*pg_query.ParamRef); ok {
			nums = append(nums, pr.Number)
			return false
		}
		return true
	})
	return nums
}

// COPY requires one VALUES row of placeholders in column order and cannot represent
// expressions or ON CONFLICT. Other shapes use sequential execution to preserve SQL
// semantics.
func insertValuesAllParams(stmt execStmt) bool {
	n, ok := stmt.stmtNode.GetNode().(*pg_query.Node_InsertStmt)
	if !ok {
		return false
	}
	if n.InsertStmt.GetOnConflictClause() != nil {
		return false
	}
	// An INSERT with no explicit column list ("INSERT INTO t VALUES
	// (...)", valid SQL — Postgres infers columns positionally from the
	// table definition) has nothing for CopyFrom's own columnNames
	// argument: pgx.Conn.CopyFrom builds its COPY command as
	// "copy tablename (<columnNames>) from stdin binary", and an empty
	// columnNames slice produces "copy tablename () from stdin binary" —
	// a Postgres syntax error, not merely an unsupported shape.
	cols := n.InsertStmt.GetCols()
	if len(cols) == 0 {
		return false
	}
	sel, ok := n.InsertStmt.GetSelectStmt().GetNode().(*pg_query.Node_SelectStmt)
	if !ok {
		return false
	}
	valuesLists := sel.SelectStmt.GetValuesLists()
	if len(valuesLists) != 1 {
		return false
	}
	list, ok := valuesLists[0].GetNode().(*pg_query.Node_List)
	if !ok {
		return false
	}
	items := list.List.GetItems()
	if len(items) != len(cols) {
		return false
	}
	for i, item := range items {
		pr, ok := item.GetNode().(*pg_query.Node_ParamRef)
		if !ok || pr.ParamRef.GetNumber() != int32(i+1) {
			return false
		}
	}
	return true
}

// insertColumnNames extracts an INSERT statement's own column list from
// its already-parsed tree — nothing in execStmt exposes this today
// (ReturningList is the only list it carries), so this reads it directly
// off stmt.stmtNode rather than adding a new execStmt field only the
// COPY path needs.
func insertColumnNames(stmt execStmt) []string {
	n, ok := stmt.stmtNode.GetNode().(*pg_query.Node_InsertStmt)
	if !ok {
		return nil
	}
	cols := n.InsertStmt.GetCols()
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.GetResTarget().GetName()
	}
	return names
}

// insertPrimaryKeyColumn resolves table's declared primary-key column
// name from modCtx's own model declarations, independent of whether the
// table participates in audit logging — unlike resolveAuditedExecTable,
// which also requires an audited_tables[] registration. The COPY fast
// path needs the pk's name to correlate copied rows back in its own
// post-copy read-back whenever opts.returning is requested, a need that
// exists regardless of opts.skip_audit.
func insertPrimaryKeyColumn(modCtx *ModuleContext, table string) (string, bool) {
	for _, decl := range modCtx.ModelDecls() {
		if modeltable.Name(decl) != table {
			continue
		}
		return primaryKeyColumn(decl)
	}
	return "", false
}

// wrapBatchFailure wraps a fast-path failure no single param_sets entry
// caused in host.db.exec_batch's db.batch_error envelope, with index -1
// (host-abi-reference.md §5).
func wrapBatchFailure(hostErr *abiv1.HostError) *abiv1.HostError {
	if hostErr.Code == abiv1.ErrCodeDBBatchError {
		return hostErr
	}
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeDBBatchError,
		Message: hostErr.Message,
		Details: map[string]any{"index": -1, "code": hostErr.Code, "message": hostErr.Message, "details": hostErr.Details},
	}
}

// execBatchCopy runs a COPY-eligible INSERT batch (per resolveCopyPlan)
// via Postgres's COPY protocol instead of one INSERT per parameter set.
func execBatchCopy(ctx context.Context, primary *sql.DB, modCtx *ModuleContext, p preparedExec, input abiv1.DBExecBatchInput, plan copyPlan) (abiv1.DBExecBatchOutput, *abiv1.HostError) {
	conn, tx, finish, hostErr := beginOrBorrowExecTx(ctx, primary, modCtx, input.TxID)
	if hostErr != nil {
		return abiv1.DBExecBatchOutput{}, hostErr
	}

	start := time.Now()
	var (
		rowsCopied int64
		newRows    []map[string]any
	)
	hostErr = withTenantRole(ctx, tx, modCtx, func() *abiv1.HostError {
		if hostErr := p.validateReturning(ctx, tx); hostErr != nil {
			return hostErr
		}

		copyErr := conn.Raw(func(driverConn any) error {
			pgxConn := driverConn.(*stdlib.Conn).Conn()
			n, err := pgxConn.CopyFrom(ctx, pgx.Identifier{p.table}, plan.Columns, pgx.CopyFromRows(input.ParamSets))
			rowsCopied = n
			return err
		})
		if copyErr != nil {
			return translateExecError(copyErr)
		}
		if plan.Readback {
			var readbackErr *abiv1.HostError
			newRows, readbackErr = copyReadback(ctx, tx, p, plan, input.ParamSets)
			return readbackErr
		}
		return nil
	})
	if hostErr != nil {
		_ = finish(errors.New(hostErr.Message))
		return abiv1.DBExecBatchOutput{}, wrapBatchFailure(hostErr)
	}

	var returning [][]any
	if plan.Readback {
		if p.audited {
			if err := writeAuditForExec(ctx, tx, modCtx, p.table, p.stmt, p.pkCol, p.excludeCols, nil, newRows); err != nil {
				_ = finish(err)
				return abiv1.DBExecBatchOutput{}, wrapBatchFailure(&abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()})
			}
		}
		if p.requestedCols != nil {
			returning = projectReturning(newRows, p.requestedCols)
		}
	}

	duration, hostErr := finishBatchTx(finish, start, modCtx, input.SQL, len(input.ParamSets), "host.db.exec_batch: slow COPY batch")
	if hostErr != nil {
		return abiv1.DBExecBatchOutput{}, hostErr
	}
	return batchOutput(int(rowsCopied), duration, returning, p.requestedCols), nil
}

// maxReadbackChunkParams caps how many primary-key values a single
// post-copy read-back SELECT binds at once — one bound parameter per
// row (copyReadbackChunk's own UNION ALL branches each reference their
// row's pk value exactly once), comfortably clear of Postgres's
// 65535-bound-parameters-per-statement limit.
const maxReadbackChunkParams = 5000

// COPY cannot return rows directly, so copyReadback queries supplied primary keys for
// auditing and returning. Chunking keeps an otherwise valid COPY batch from exceeding
// PostgreSQL's parameter limit during readback.
func copyReadback(ctx context.Context, tx *sql.Tx, p preparedExec, plan copyPlan, paramSets [][]any) ([]map[string]any, *abiv1.HostError) {
	pkIdx := slices.Index(plan.Columns, plan.PKCol)

	var allRows []map[string]any
	for start := 0; start < len(paramSets); start += maxReadbackChunkParams {
		end := min(start+maxReadbackChunkParams, len(paramSets))
		rows, hostErr := copyReadbackChunk(ctx, tx, p, plan, pkIdx, paramSets[start:end])
		if hostErr != nil {
			return nil, hostErr
		}
		allRows = append(allRows, rows...)
	}
	return allRows, nil
}

// copyReadbackChunk runs one read-back SELECT for a single chunk of
// paramSets (see copyReadback's own chunking rationale) — pkIdx is the
// pk column's position within plan.Columns, precomputed once by the
// caller since it's the same for every chunk.
func copyReadbackChunk(ctx context.Context, tx *sql.Tx, p preparedExec, plan copyPlan, pkIdx int, paramSets [][]any) ([]map[string]any, *abiv1.HostError) {
	// Each branch compares its key parameter directly with the typed column;
	// parameters used only in a VALUES list would resolve to text. The ordinal
	// restores input order while the composite keeps the sort row narrow.
	pkIdent := pgx.Identifier{plan.PKCol}.Sanitize()
	tableIdent := pgx.Identifier{p.table}.Sanitize()

	branches := make([]string, len(paramSets))
	pkValues := make([]any, len(paramSets))
	for i, params := range paramSets {
		pkValues[i] = params[pkIdx]
		branches[i] = fmt.Sprintf("SELECT t AS row_data, %d AS copy_readback_seq FROM %s t WHERE t.%s = $%d", i, tableIdent, pkIdent, i+1)
	}

	selectSQL := fmt.Sprintf("SELECT (x.row_data).* FROM (%s) x ORDER BY x.copy_readback_seq", strings.Join(branches, " UNION ALL "))

	rows, err := tx.QueryContext(ctx, selectSQL, pkValues...)
	if err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeExecError, Message: err.Error()}
	}
	newRows, err := scanRowsToMaps(rows)
	if err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeExecError, Message: err.Error()}
	}
	return newRows, nil
}

// pipelineRowError carries one pipelined statement's own failure back out
// of conn.Raw's plain error-returning callback, preserving the row index
// and structured HostError that a bare error can't.
type pipelineRowError struct {
	index int
	host  *abiv1.HostError
}

func (e *pipelineRowError) Error() string { return fmt.Sprintf("row %d: %s", e.index, e.host.Message) }

// Native pgx scanning returns UUID arrays and pgtype wrappers that differ from
// database/sql scalars. Normalize them before encoding so UUIDs remain strings and
// wrappers do not lose unexported fields.
func normalizePgxRow(row map[string]any) {
	for col, val := range row {
		switch v := val.(type) {
		case [16]byte:
			row[col] = uuid.UUID(v).String()
		case driver.Valuer:
			if dv, err := v.Value(); err == nil {
				row[col] = dv
			}
		}
	}
}

// pipelineRowResult carries raw RETURNING rows and affected counts outside conn.Raw. Audit
// writes need sql.Tx, which must not be used inside that callback.
type pipelineRowResult struct {
	rowsAffected int64
	newRows      []map[string]any
}

// Audit pre-reads precede all pipelined writes, so eligibility excludes
// repeated targets whose intermediate values must appear in the audit trail.
func execBatchPipeline(ctx context.Context, primary *sql.DB, modCtx *ModuleContext, p preparedExec, input abiv1.DBExecBatchInput) (abiv1.DBExecBatchOutput, *abiv1.HostError) {
	conn, tx, finish, hostErr := beginOrBorrowExecTx(ctx, primary, modCtx, input.TxID)
	if hostErr != nil {
		return abiv1.DBExecBatchOutput{}, hostErr
	}

	start := time.Now()

	var (
		oldRowsPerIndex [][]map[string]any
		results         = make([]pipelineRowResult, len(input.ParamSets))
	)
	hostErr = withTenantRole(ctx, tx, modCtx, func() *abiv1.HostError {
		if hostErr := p.validateReturning(ctx, tx); hostErr != nil {
			return batchErrorForHostErr(0, hostErr)
		}

		if p.audited {
			rows, err := captureRowsBeforeExecBatch(ctx, tx, auditableExecStmt{
				Operation: p.stmt.Operation, Table: p.table, Relation: p.stmt.Relation, WhereClause: p.stmt.WhereClause,
			}, input.ParamSets)
			if err != nil {
				return batchErrorForRowErr(-1, abiv1.ErrCodeExecError, "", err)
			}
			oldRowsPerIndex = rows
		}
		return sendPipelineBatch(ctx, conn, p, input.ParamSets, results)
	})
	if hostErr != nil {
		_ = finish(errors.New(hostErr.Message))
		return abiv1.DBExecBatchOutput{}, wrapBatchFailure(hostErr)
	}

	if p.audited {
		// Pair audit rows within each statement; overlapping primary keys across
		// statements must retain separate entries.
		entries := make([]auditLogEntry, 0, len(results))
		for i, res := range results {
			newRows := res.newRows
			if p.stmt.Operation == "DELETE" {
				// new_data must stay NULL for a DELETE regardless of
				// whether opts.returning requested rows back for the
				// module's own purposes — matches writeAuditForExec's
				// own DELETE handling (host_db_exec.go).
				newRows = nil
			}
			entries = append(entries, pairAuditEntries(p.pkCol, oldRowsPerIndex[i], newRows)...)
		}
		if err := insertAuditLogRows(ctx, tx, modCtx, p.table, p.stmt.Operation, p.excludeCols, entries); err != nil {
			_ = finish(err)
			return abiv1.DBExecBatchOutput{}, batchErrorForRowErr(-1, abiv1.ErrCodeUnavailable, "audit write failed: ", err)
		}
	}

	var totalRowsAffected int
	var returning [][]any
	for _, res := range results {
		totalRowsAffected += int(res.rowsAffected)
		if p.requestedCols != nil {
			returning = append(returning, projectReturning(res.newRows, p.requestedCols)...)
		}
	}

	duration, hostErr := finishBatchTx(finish, start, modCtx, input.SQL, len(input.ParamSets), "host.db.exec_batch: slow pipelined batch")
	if hostErr != nil {
		return abiv1.DBExecBatchOutput{}, hostErr
	}
	return batchOutput(totalRowsAffected, duration, returning, p.requestedCols), nil
}

// sendPipelineBatch sends one p.finalSQL per parameter set via pgx's
// SendBatch and records each one's raw result in results.
func sendPipelineBatch(ctx context.Context, conn *sql.Conn, p preparedExec, paramSets [][]any, results []pipelineRowResult) *abiv1.HostError {
	batch := &pgx.Batch{}
	for _, params := range paramSets {
		batch.Queue(p.finalSQL, params...)
	}

	// conn.Raw holds the underlying connection's own mutex for its whole
	// callback (database/sql's Conn.Raw) — the same mutex tx.ExecContext
	// needs, so the caller's audit writes happen strictly after this call
	// returns, never inside it.
	pipelineErr := conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()
		br := pgxConn.SendBatch(ctx, batch)
		defer func() { _ = br.Close() }()

		for i := range paramSets {
			var newRows []map[string]any
			var rowsAffected int64

			if p.needReturning {
				rows, err := br.Query()
				if err != nil {
					return &pipelineRowError{index: i, host: translateExecError(err)}
				}
				newRows, err = pgx.CollectRows(rows, pgx.RowToMap)
				if err != nil {
					return &pipelineRowError{index: i, host: translateExecError(err)}
				}
				for _, row := range newRows {
					normalizePgxRow(row)
				}
				rowsAffected = int64(len(newRows))
			} else {
				tag, err := br.Exec()
				if err != nil {
					return &pipelineRowError{index: i, host: translateExecError(err)}
				}
				rowsAffected = tag.RowsAffected()
			}

			// Etag-checked updates are excluded before pipelining: once SendBatch flushes
			// the statements, detecting a mismatch cannot stop later rows.
			results[i] = pipelineRowResult{rowsAffected: rowsAffected, newRows: newRows}
		}
		return nil
	})

	if pipelineErr != nil {
		if rowErr, ok := errors.AsType[*pipelineRowError](pipelineErr); ok {
			return batchErrorForHostErr(rowErr.index, rowErr.host)
		}
		return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: pipelineErr.Error(), Retry: true}
	}
	return nil
}
