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

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// copyEligibleRowThreshold is host-abi-reference.md's own documented
// floor for host.db.exec_batch's INSERT-only COPY fast path ("Performance
// notes": "For INSERT-only workloads with > 100 rows").
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

// resolveCopyPlan decides whether p's batch (already confirmed to parse
// as a single INSERT by prepareExec) can use Postgres's COPY protocol
// instead of goerp#512's sequential per-row path. It takes no opinion on
// opts.continue_on_error — see DBExecBatch's own dispatch comment
// (host_db_exec_batch.go) for how that's handled: attempting the fast
// path regardless and retrying sequentially on failure when safe to do
// so, rather than excluding it here. sdk/go/db.ExecBatch — the only Go
// SDK entry point — always sends continue_on_error: true; excluding it
// here would make this fast path unreachable from that SDK entirely.
//
// A post-COPY read-back (by primary key, since COPY carries no RETURNING
// clause at the protocol level) is needed whenever audit logging will run
// for these rows, or the caller requested opts.returning — and is only
// possible when the primary key's value is actually known, i.e. present
// in the INSERT's own column list (a DB-generated pk, e.g. a UUID
// default, is never in the caller's own column list, so such a batch
// falls back to the sequential path's own RETURNING-based capture
// instead). The primary-key column's *name* is resolved independently of
// opts.skip_audit — unlike prepareExec's own audit-gated resolution —
// since a caller can skip audit writes but still request opts.returning,
// which needs the same pk-based correlation.
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

// pipelineEligible reports whether p's batch (an UPDATE or DELETE) can
// use pgx pipelining instead of the sequential path.
//
// opts.continue_on_error does not rule pipelining out — see
// resolveCopyPlan's own doc comment for why (DBExecBatch attempts the
// fast path regardless and falls back to a full sequential re-run only
// on failure, since a failed pipeline commits nothing either). A
// single-statement batch is excluded: pipelining one statement has no
// benefit over calling it directly and only adds SendBatch's own
// bookkeeping overhead. An audited table whose batch targets the same
// row more than once is excluded too — see
// pipelineHasDuplicateAuditTargets's own doc comment for why.
//
// An etag-checked UPDATE (p.hadEtagCheck) is excluded unconditionally,
// regardless of continue_on_error or any retry: pgx's SendBatch flushes
// every queued statement to Postgres before this code reads any result
// back, so an etag mismatch — a zero-rows-affected success, not a
// Postgres error — never aborts the transaction the way a real
// constraint violation does. Every later statement in the batch still
// executes server-side even though the mismatch is reported at an
// earlier index, unlike the sequential path's own execRow, which stops
// immediately on the first failure. A retry-on-failure strategy can't
// fix this the way it fixes the continue_on_error case above: the
// unwanted writes already happened inside the fast attempt's own
// transaction by the time a mismatch is even detected, and on a
// borrowed transaction that attempt's own finish is a no-op, so nothing
// rolls them back before the retry.
func pipelineEligible(p preparedExec, paramSets [][]any) bool {
	if (p.stmt.Operation != "UPDATE" && p.stmt.Operation != "DELETE") || len(paramSets) <= 1 {
		return false
	}
	if p.hadEtagCheck {
		return false
	}
	if p.audited && pipelineHasDuplicateAuditTargets(p, paramSets) {
		return false
	}
	return true
}

// pipelineHasDuplicateAuditTargets reports whether p's own WHERE clause
// targets the same row more than once across paramSets. The pipeline
// path's audit pre-read (execBatchPipeline) reads every row's "before"
// state up front, before any statement in the batch runs — unlike the
// sequential path's own execRow, which re-reads immediately before each
// row's own write. If the same row appears twice, the second entry's
// audit old_data would show the pre-batch original value instead of the
// first entry's just-applied change: the table's own final data is still
// correct either way, but the audit trail wouldn't be. RETURNING/etag
// correctness for a duplicate target is unaffected, so this only guards
// the audited case.
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

// insertValuesAllParams reports whether stmt's own VALUES clause is
// exactly one row consisting entirely of parameter placeholders, each in
// the same position as its own column ($1 for column 0, $2 for column 1,
// ...) — the only shape COPY can represent, since COPY's wire protocol
// carries literal row data positionally aligned to a column list, not
// arbitrary SQL expressions or an independently-ordered placeholder
// sequence:
//   - An INSERT like "VALUES ($1, gen_random_uuid(), $2)" (a placeholder
//     mixed with a computed expression) can never use COPY: there's no
//     way to synthesize gen_random_uuid()'s own value into a COPY row
//     without evaluating it some other way first, and this ABI has no
//     mechanism for that.
//   - An INSERT like "(id, tenant_id, name) VALUES ($2, $1, $3)" (valid
//     SQL — params bound out of column order) would silently misalign
//     execBatchCopy's own paramSets-is-already-column-ordered assumption
//     if allowed through: paramSets[i] is indexed by placeholder number
//     ($1, $2, ...), not by column position, so accepting an
//     out-of-order VALUES list here would write each row's values into
//     the wrong columns without any error.
//
// Also rejects an ON CONFLICT clause outright — COPY has no equivalent,
// so an upsert/ignore-duplicate INSERT falls back to the sequential path,
// which actually preserves that behavior, rather than surfacing every
// conflicting row as a hard unique_violation.
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
		if tableNameForORM(decl) != table {
			continue
		}
		return primaryKeyColumn(decl)
	}
	return "", false
}

// wrapCopyBatchFailure wraps hostErr as host.db.exec_batch's own
// documented db.batch_error envelope ({index, code, message, details} —
// host-abi-reference.md §5), for any failure execBatchCopy can produce —
// the COPY step itself, its post-copy read-back, or the audit-log write
// that read-back feeds — not just the COPY step's own, so a caller
// branching on Code == "db.batch_error" or reading Details["index"]
// doesn't have to special-case which internal step actually failed.
// index is -1 for the same reason the COPY step's own failure uses it:
// none of these failures are attributable to one specific param_sets
// entry the way a sequential row failure is.
func wrapCopyBatchFailure(hostErr *abi.HostError) *abi.HostError {
	if hostErr.Code == abi.ErrCodeDBBatchError {
		return hostErr
	}
	return &abi.HostError{
		Code:    abi.ErrCodeDBBatchError,
		Message: hostErr.Message,
		Details: map[string]any{"index": -1, "code": hostErr.Code, "message": hostErr.Message, "details": hostErr.Details},
	}
}

// execBatchCopy runs a COPY-eligible INSERT batch (per resolveCopyPlan)
// via Postgres's COPY protocol instead of one INSERT per parameter set.
func execBatchCopy(ctx context.Context, primary *sql.DB, modCtx *ModuleContext, p preparedExec, input dbExecBatchInput, plan copyPlan) (dbExecBatchOutput, *abi.HostError) {
	conn, tx, finish, hostErr := beginOrBorrowExecTx(ctx, primary, modCtx, input.TxID)
	if hostErr != nil {
		return dbExecBatchOutput{}, hostErr
	}

	start := time.Now()
	var rowsCopied int64
	copyErr := conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()
		n, err := pgxConn.CopyFrom(ctx, pgx.Identifier{p.table}, plan.Columns, pgx.CopyFromRows(input.ParamSets))
		rowsCopied = n
		return err
	})
	if copyErr != nil {
		_ = finish(copyErr)
		return dbExecBatchOutput{}, wrapCopyBatchFailure(translateExecError(copyErr))
	}

	var returning [][]any
	if plan.Readback {
		newRows, hostErr := copyReadback(ctx, tx, p, plan, input.ParamSets)
		if hostErr != nil {
			_ = finish(errors.New(hostErr.Message))
			return dbExecBatchOutput{}, wrapCopyBatchFailure(hostErr)
		}
		if p.audited {
			if err := writeAuditForExec(ctx, tx, modCtx, p.table, p.stmt, p.pkCol, p.excludeCols, nil, newRows); err != nil {
				_ = finish(err)
				return dbExecBatchOutput{}, wrapCopyBatchFailure(&abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()})
			}
		}
		if p.requestedCols != nil {
			returning = projectReturning(newRows, p.requestedCols)
		}
	}

	if err := finish(nil); err != nil {
		return dbExecBatchOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}
	duration := time.Since(start)

	if duration > slowQueryThreshold {
		log.Warn().Str("module", modCtx.ModuleName).Str("sql", input.SQL).
			Int("param_sets", len(input.ParamSets)).Dur("duration", duration).
			Msg("host.db.exec_batch: slow COPY batch")
	}

	output := dbExecBatchOutput{
		TotalRowsAffected: int(rowsCopied),
		DurationMs:        float64(duration.Microseconds()) / 1000,
	}
	if p.requestedCols != nil {
		output.Returning = returning
	}
	return output, nil
}

// maxReadbackChunkParams caps how many primary-key values a single
// post-copy read-back SELECT binds at once. Postgres rejects any
// statement needing more than 65535 bound parameters (each pk value is
// bound twice in copyReadbackChunk's own query text — once for the
// IN-list, once for its CASE/WHEN ordering branch — but a value bound
// twice in one statement's text still counts as one parameter, so this
// cap bounds len(paramSets) directly, comfortably clear of that limit).
const maxReadbackChunkParams = 5000

// copyReadback re-queries the rows execBatchCopy just copied, by their
// own primary-key values (taken directly from paramSets — every copied
// row's pk value is a plain input the caller supplied, per
// resolveCopyPlan's own eligibility rule), for whichever of audit-log
// writing or opts.returning needs row data COPY's own wire protocol
// cannot return directly. Chunked at maxReadbackChunkParams rather than
// one statement for the whole batch — the COPY itself has no such limit,
// so a batch large enough to need chunking here would otherwise succeed
// at the COPY step and then hard-fail at read-back, undoing otherwise
// valid work.
func copyReadback(ctx context.Context, tx *sql.Tx, p preparedExec, plan copyPlan, paramSets [][]any) ([]map[string]any, *abi.HostError) {
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
func copyReadbackChunk(ctx context.Context, tx *sql.Tx, p preparedExec, plan copyPlan, pkIdx int, paramSets [][]any) ([]map[string]any, *abi.HostError) {
	pkValues := make([]any, len(paramSets))
	inPlaceholders := make([]string, len(paramSets))
	// A plain "WHERE pk IN (...)" gives Postgres no ordering guarantee —
	// this ABI's own contract requires opts.returning rows back in
	// param_sets order (see host-abi-reference.md's own ABI-level output
	// shape). A CASE/WHEN over the same placeholders sorts by that order
	// entirely in SQL, using each pk value's own already-correct type via
	// ordinary "=" comparison — the same reasoning behind not binding
	// pkValues as a single Postgres array parameter: paramSets can carry
	// any pk type (uuid, text, int, ...), and database/sql has no generic
	// way to bind a heterogeneous []any as one typed array parameter.
	// Known scope boundary: this costs Postgres one CASE/WHEN branch
	// evaluation per row per WHEN clause (up to maxReadbackChunkParams²
	// comparisons worst-case per chunk) to compute the sort order — a
	// join against an explicit ordinal list would let the planner use a
	// hash or merge join instead, without changing the bind shape, but
	// isn't implemented here.
	caseWhens := make([]string, len(paramSets))
	for i, params := range paramSets {
		pkValues[i] = params[pkIdx]
		placeholder := fmt.Sprintf("$%d", i+1)
		inPlaceholders[i] = placeholder
		caseWhens[i] = fmt.Sprintf("WHEN %s THEN %d", placeholder, i)
	}

	pkIdent := pgx.Identifier{plan.PKCol}.Sanitize()
	selectSQL := fmt.Sprintf("SELECT * FROM %s WHERE %s IN (%s) ORDER BY CASE %s %s END",
		pgx.Identifier{p.table}.Sanitize(), pkIdent, strings.Join(inPlaceholders, ","), pkIdent, strings.Join(caseWhens, " "))

	rows, err := tx.QueryContext(ctx, selectSQL, pkValues...)
	if err != nil {
		return nil, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
	}
	if p.requestedCols != nil {
		available, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return nil, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
		}
		if err := validateRequestedColumns(p.requestedCols, available); err != nil {
			_ = rows.Close()
			return nil, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
		}
	}
	newRows, err := scanRowsToMaps(rows)
	if err != nil {
		return nil, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
	}
	return newRows, nil
}

// pipelineRowError carries one pipelined statement's own failure back out
// of conn.Raw's plain error-returning callback, preserving the row index
// and structured HostError that a bare error can't.
type pipelineRowError struct {
	index int
	host  *abi.HostError
}

func (e *pipelineRowError) Error() string { return fmt.Sprintf("row %d: %s", e.index, e.host.Message) }

// normalizePgxRow rewrites any value in row that pgx's own native
// row-to-map scanning (pgx.RowToMap, used by the pipeline path's own
// RETURNING handling since it runs against raw pgx.Rows, not *sql.Rows)
// returns in a shape different from what every other host.db.* RETURNING/
// query path produces via scanRowsToMaps (*sql.Rows, database/sql's
// generic driver.Value conversion — always a plain scalar: string,
// int64, float64, bool, []byte, time.Time, or nil):
//   - uuid columns decode to a bare [16]byte in pgx's own native type
//     mapping, unambiguously (Postgres's bytea codec always decodes to
//     []byte, a slice, never a fixed [16]byte array, so this type alone
//     identifies a uuid value with no risk of misclassifying some other
//     column) — reformatted to the same string every other uuid value
//     this ABI returns.
//   - Any other type pgx decodes into its own pgtype wrapper (e.g.
//     pgtype.Numeric for NUMERIC/DECIMAL, wrapping an arbitrary-precision
//     *big.Int with unexported fields) is run through its own Value()
//     method (database/sql/driver.Valuer, which every pgtype exposing a
//     database/sql-compatible representation implements) — the same
//     conversion database/sql itself would have applied, so msgpack
//     never has to encode an unexported-field struct it would otherwise
//     silently drop.
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

// pipelineRowResult is one queued statement's own raw result, collected
// inside conn.Raw's callback for interpretation afterward — RETURNING
// rows and the affected-row count, but not yet an audit write (that
// needs tx, which conn.Raw's own callback must never touch — see
// execBatchPipeline's own doc comment).
type pipelineRowResult struct {
	rowsAffected int64
	newRows      []map[string]any
}

// execBatchPipeline runs a pipeline-eligible UPDATE/DELETE batch (per
// pipelineEligible) via pgx's SendBatch instead of one round trip per
// parameter set. Every queued statement shares p.finalSQL — the same
// template, RETURNING clause included when p.needReturning, that the
// sequential path's own execRow runs per row — so only how the N
// statements are sent and their results collected changes; RETURNING/
// etag/audit interpretation per result reuses the sequential path's own
// helpers (isEtagMismatch, writeAuditForExec) unchanged.
//
// Each row's own "before" state (audited UPDATE/DELETE) is still
// captured sequentially via captureRowsBeforeExec, exactly as the
// sequential path does per row — pipelining only changes how the batch's
// write statements themselves are sent, not this existing pre-read.
// Known scope boundary: for an audited table this pre-read stays one
// round trip per row rather than a single batched read (the way
// copyReadback batches the COPY path's own post-write read), since a
// general batched pre-read would need to interpret an arbitrary WHERE
// clause rather than a known primary-key equality — undercutting
// pipelining's own round-trip savings for audited UPDATE/DELETE batches
// specifically; unaudited batches (no pre-read at all) get the full
// benefit already.
func execBatchPipeline(ctx context.Context, primary *sql.DB, modCtx *ModuleContext, p preparedExec, input dbExecBatchInput) (dbExecBatchOutput, *abi.HostError) {
	conn, tx, finish, hostErr := beginOrBorrowExecTx(ctx, primary, modCtx, input.TxID)
	if hostErr != nil {
		return dbExecBatchOutput{}, hostErr
	}

	start := time.Now()

	oldRowsPerIndex := make([][]map[string]any, len(input.ParamSets))
	if p.audited {
		for i, params := range input.ParamSets {
			rows, err := captureRowsBeforeExec(ctx, tx, auditableExecStmt{
				Operation: p.stmt.Operation, Table: p.table, Relation: p.stmt.Relation, WhereClause: p.stmt.WhereClause,
			}, params)
			if err != nil {
				_ = finish(err)
				return dbExecBatchOutput{}, &abi.HostError{
					Code:    abi.ErrCodeDBBatchError,
					Message: fmt.Sprintf("parameter set %d: %s", i, err.Error()),
					Details: map[string]any{"index": i, "code": abi.ErrCodeExecError, "message": err.Error()},
				}
			}
			oldRowsPerIndex[i] = rows
		}
	}

	batch := &pgx.Batch{}
	for _, params := range input.ParamSets {
		batch.Queue(p.finalSQL, params...)
	}

	// conn.Raw holds the underlying connection's own mutex for its whole
	// callback (database/sql's Conn.Raw) — the same mutex tx.ExecContext
	// needs, so writeAuditForExec (which runs one below, per row) must
	// happen strictly after this call returns, never inside it. This
	// closure only sends the batch and collects each row's own raw
	// result; audit writes happen in a second pass below.
	results := make([]pipelineRowResult, len(input.ParamSets))
	pipelineErr := conn.Raw(func(driverConn any) error {
		pgxConn := driverConn.(*stdlib.Conn).Conn()
		br := pgxConn.SendBatch(ctx, batch)
		defer func() { _ = br.Close() }()

		for i := range input.ParamSets {
			var newRows []map[string]any
			var rowsAffected int64

			if p.needReturning {
				rows, err := br.Query()
				if err != nil {
					return &pipelineRowError{index: i, host: translateExecError(err)}
				}
				// Every queued statement shares the same template
				// (p.finalSQL), so its RETURNING column set is identical
				// across every row — validated once, from the first
				// row's own field descriptions, rather than redundantly
				// on every one of the batch's N rows.
				if i == 0 && p.requestedCols != nil {
					available := make([]string, len(rows.FieldDescriptions()))
					for j, f := range rows.FieldDescriptions() {
						available[j] = f.Name
					}
					if err := validateRequestedColumns(p.requestedCols, available); err != nil {
						return &pipelineRowError{index: i, host: &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}}
					}
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

			// No etag-mismatch check here: pipelineEligible already
			// excludes every batch where p.hadEtagCheck is true, so
			// isEtagMismatch could never fire — and even if it somehow
			// did, pgx's SendBatch has already flushed every queued
			// statement to Postgres by this point, so detecting a
			// mismatch here would be too late to stop later rows from
			// executing (see pipelineEligible's own doc comment). That
			// exclusion, not a check in this loop, is what keeps
			// pipelining safe for etag-checked updates.
			results[i] = pipelineRowResult{rowsAffected: rowsAffected, newRows: newRows}
		}
		return nil
	})

	if pipelineErr != nil {
		_ = finish(pipelineErr)
		if rowErr, ok := errors.AsType[*pipelineRowError](pipelineErr); ok {
			return dbExecBatchOutput{}, &abi.HostError{
				Code:    abi.ErrCodeDBBatchError,
				Message: fmt.Sprintf("parameter set %d: %s", rowErr.index, rowErr.host.Message),
				Details: map[string]any{"index": rowErr.index, "code": rowErr.host.Code, "message": rowErr.host.Message, "details": rowErr.host.Details},
			}
		}
		return dbExecBatchOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: pipelineErr.Error(), Retry: true}
	}

	if p.audited {
		for i, res := range results {
			if err := writeAuditForExec(ctx, tx, modCtx, p.table, p.stmt, p.pkCol, p.excludeCols, oldRowsPerIndex[i], res.newRows); err != nil {
				_ = finish(err)
				return dbExecBatchOutput{}, &abi.HostError{
					Code:    abi.ErrCodeDBBatchError,
					Message: fmt.Sprintf("parameter set %d: audit write failed: %s", i, err.Error()),
					Details: map[string]any{"index": i, "code": abi.ErrCodeUnavailable, "message": err.Error()},
				}
			}
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

	if err := finish(nil); err != nil {
		return dbExecBatchOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}
	duration := time.Since(start)

	if duration > slowQueryThreshold {
		log.Warn().Str("module", modCtx.ModuleName).Str("sql", input.SQL).
			Int("param_sets", len(input.ParamSets)).Dur("duration", duration).
			Msg("host.db.exec_batch: slow pipelined batch")
	}

	output := dbExecBatchOutput{
		TotalRowsAffected: totalRowsAffected,
		DurationMs:        float64(duration.Microseconds()) / 1000,
	}
	if p.requestedCols != nil {
		output.Returning = returning
	}
	return output, nil
}
