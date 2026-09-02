package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Audit-log-injection mechanism for host.db.exec (goerp#460, not yet
// built — this ticket lands first per the goerp#60 tracking issue).
// host.db.exec should call resolveAuditedExecTable once it parses the
// module's SQL, then, for an audited table, captureRowsBeforeExec before
// running the statement and writeExecAuditEntries after (gated on
// opts.skip_audit, which belongs to host.db.exec's own ABI input).

// auditableExecStmt is what captureRowsBeforeExec needs from a parsed
// UPDATE or DELETE statement.
type auditableExecStmt struct {
	Operation   string // "UPDATE" or "DELETE" — matches audit_log.operation's CHECK constraint
	Table       string
	Relation    *pg_query.RangeVar
	WhereClause *pg_query.Node // nil for an unconditional UPDATE/DELETE
}

// parseAuditableExecStmt extracts stmt from tree's single statement. An
// INSERT (no old row to capture) or anything else is ok=false.
func parseAuditableExecStmt(tree *pg_query.ParseResult) (stmt auditableExecStmt, ok bool) {
	stmts := tree.GetStmts()
	if len(stmts) != 1 {
		return auditableExecStmt{}, false
	}

	switch n := stmts[0].GetStmt().GetNode().(type) {
	case *pg_query.Node_UpdateStmt:
		rel := n.UpdateStmt.GetRelation()
		return auditableExecStmt{Operation: "UPDATE", Table: rel.GetRelname(), Relation: rel, WhereClause: n.UpdateStmt.GetWhereClause()}, true
	case *pg_query.Node_DeleteStmt:
		rel := n.DeleteStmt.GetRelation()
		return auditableExecStmt{Operation: "DELETE", Table: rel.GetRelname(), Relation: rel, WhereClause: n.DeleteStmt.GetWhereClause()}, true
	default:
		return auditableExecStmt{}, false
	}
}

// resolveAuditedExecTable resolves table (a bare name from raw SQL)
// against modCtx's declared models and audited_tables[] registry.
// audited is false if no declared model owns table, that model has no
// primary key, or the table isn't declared audited.
func resolveAuditedExecTable(modCtx *ModuleContext, table string) (pkCol string, excludeCols map[string]bool, audited bool) {
	reg := modCtx.DataAuditRegistry()
	if reg == nil {
		return "", nil, false
	}

	for _, decl := range modCtx.ModelDecls() {
		if tableNameForORM(decl) != table {
			continue
		}
		pk, ok := primaryKeyColumn(decl)
		if !ok {
			return "", nil, false
		}
		cols, isAudited := reg.Lookup(modCtx.ModuleName + "." + decl.Name)
		if !isAudited {
			return "", nil, false
		}
		return pk, cols, true
	}
	return "", nil, false
}

// captureRowsBeforeExec reads, within tx, the current values of every
// row stmt's WHERE clause matches. A plain read, same as
// fetchRowForAuditBeforeWrite's own — no FOR UPDATE lock, so a
// concurrent commit between this read and the caller's own write can
// leave old_data stale relative to what that write actually overwrote;
// closing that race is etag enforcement's job (goerp#458), not this
// mechanism's. renumberParams is needed because a WHERE clause lifted
// out of a larger statement can skip $n numbers used only in that
// statement's SET clause, and Postgres sizes a statement's param count
// off the highest $n its own text references — a gap there fails with
// "could not determine data type of parameter".
func captureRowsBeforeExec(ctx context.Context, tx *sql.Tx, stmt auditableExecStmt, params []any) ([]map[string]any, error) {
	whereClause, whereParams, err := renumberParams(stmt.WhereClause, params)
	if err != nil {
		return nil, err
	}

	selectSQL, err := deparseSelectAll(stmt.Relation, whereClause)
	if err != nil {
		return nil, fmt.Errorf("build audit pre-read query: %w", err)
	}

	rows, err := tx.QueryContext(ctx, selectSQL, whereParams...)
	if err != nil {
		return nil, fmt.Errorf("read rows before audited exec: %w", err)
	}
	return scanRowsToMaps(rows)
}

// renumberParams deep-clones node and rewrites every ParamRef.Number to
// a contiguous 1-based sequence (first-appearance order), returning the
// clone and the params subset each new number maps to. (nil, nil, nil) if
// node is nil. Walks via walkPGQueryTree (host_db_exec_pgquery_walk.go)
// rather than a type switch over every SQL expression node a WHERE
// clause can contain. Errors rather than panicking on a $n beyond
// len(params) — params comes from the module's own exec call, an
// untrusted boundary input, not something this function can assume is
// well-formed.
func renumberParams(node *pg_query.Node, params []any) (*pg_query.Node, []any, error) {
	return renumberParamsFrom(node, params, 0)
}

// renumberParamsFrom is renumberParams' own base-offset form: renumbered
// placeholders start at base+1 instead of always $1. Needed when more
// than one clause's own params must coexist as $n literals in a single
// combined statement (captureRowsBeforeExecBatch), where each row's own
// renumbered placeholders must continue on from the previous row's own
// highest number rather than every row restarting at $1.
func renumberParamsFrom(node *pg_query.Node, params []any, base int32) (*pg_query.Node, []any, error) {
	if node == nil {
		return nil, nil, nil
	}
	clone, ok := proto.Clone(node).(*pg_query.Node)
	if !ok {
		return nil, nil, nil
	}

	var newParams []any
	var walkErr error
	seen := make(map[int32]int32)

	walkPGQueryTree(clone.ProtoReflect(), func(m protoreflect.Message) bool {
		if walkErr != nil {
			return false
		}
		pr, ok := m.Interface().(*pg_query.ParamRef)
		if !ok {
			return true
		}
		if pr.Number < 1 || int(pr.Number) > len(params) {
			walkErr = fmt.Errorf("parameter $%d has no corresponding value (%d supplied)", pr.Number, len(params))
			return false
		}
		newNum, ok := seen[pr.Number]
		if !ok {
			newParams = append(newParams, params[pr.Number-1])
			newNum = base + int32(len(newParams))
			seen[pr.Number] = newNum
		}
		pr.Number = newNum
		return false // a ParamRef has no useful children to descend into
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}

	return clone, newParams, nil
}

// maxAuditPreReadChunkWeight caps how much total per-row weight
// captureRowsBeforeExecBatch builds into a single chunked SELECT —
// comfortably under Postgres's 65535-bound-parameters-per-statement
// limit, with headroom since each row's own weight (len(paramSets[i]))
// is a deliberate overestimate of its true referenced-param count (see
// captureRowsBeforeExecBatch's own doc comment). insertAuditLogRows
// (host_orm_write.go) chunks its own multi-row INSERT against a separate
// constant of the same value (maxAuditWriteChunkParams) — the two happen
// to share a value but bound structurally different things, so tuning
// one is never assumed to be safe for the other.
const maxAuditPreReadChunkWeight = 5000

// captureRowsBeforeExecBatch is captureRowsBeforeExec's own batched form:
// a few chunked SELECTs for the whole batch instead of one per row, using
// one UNION ALL branch per row — each branch selects its own row as a
// whole-row composite (row_data) tagged with its own batch index
// (batch_idx), the same technique copyReadbackChunk (host_db_exec_batch_
// fast.go) uses for the COPY path's own post-copy read-back — so the
// returned [][]map[string]any stays indexed by paramSets position exactly
// like calling captureRowsBeforeExec once per row would produce. Chunked
// at maxAuditPreReadChunkWeight total bound parameters, tracked per row
// via len(paramSets[i]) — a deliberately conservative proxy for a row's
// own true referenced-param count (renumberParamsFrom's own dedup can
// only make the real count smaller), safe to overestimate since it only
// ever makes a chunk somewhat smaller than the limit allows, never over
// it.
//
// This per-row attribution matters beyond ordering: pipelineHasDuplicate
// AuditTargets (host_db_exec_batch_fast.go) only excludes a batch whose
// rows bind *identical* WHERE-clause parameter values — two rows with
// different bound values can still resolve to overlapping physical rows
// (e.g. "salary > 50000" and "salary > 40000" both matching the same
// employee). Combining every row's own WHERE clause into one OR'd SELECT
// and returning a single flat pool of rows — relying on primary-key
// pairing (writeExecAuditEntries) to sort them back out afterward — would
// be unsafe: two different statements' own old/new rows sharing a primary
// key is exactly the overlapping-target case above, and pk-based pairing
// across statements silently collapses that batch's two independent
// audit entries into one, discarding the earlier statement's own audit
// trail entirely. Keeping each row's own old rows attributed to its own
// paramSets index, and pairing per statement (see execBatchPipeline's
// own audit-write pass), avoids that collapse regardless of what
// pipelineHasDuplicateAuditTargets does or doesn't catch.
func captureRowsBeforeExecBatch(ctx context.Context, tx *sql.Tx, stmt auditableExecStmt, paramSets [][]any) ([][]map[string]any, error) {
	oldRowsPerIndex := make([][]map[string]any, len(paramSets))

	for i := 0; i < len(paramSets); {
		var branches []string
		var chunkParams []any
		weight := 0
		for i < len(paramSets) {
			rowWeight := len(paramSets[i])
			if len(branches) > 0 && weight+rowWeight > maxAuditPreReadChunkWeight {
				break
			}
			node, rowParams, err := renumberParamsFrom(stmt.WhereClause, paramSets[i], int32(len(chunkParams)))
			if err != nil {
				return nil, fmt.Errorf("parameter set %d: %w", i, err)
			}
			branchSQL, err := deparseTaggedRowSelect(stmt, node, int32(i))
			if err != nil {
				return nil, fmt.Errorf("parameter set %d: build batched audit pre-read query: %w", i, err)
			}
			branches = append(branches, branchSQL)
			chunkParams = append(chunkParams, rowParams...)
			weight += rowWeight
			i++
		}

		selectSQL := fmt.Sprintf("SELECT (x.row_data).*, x.batch_idx FROM (%s) x", strings.Join(branches, " UNION ALL "))
		rows, err := tx.QueryContext(ctx, selectSQL, chunkParams...)
		if err != nil {
			return nil, fmt.Errorf("read rows before audited exec batch: %w", err)
		}
		chunkRows, err := scanRowsToMaps(rows)
		if err != nil {
			return nil, err
		}
		for _, row := range chunkRows {
			idx, err := popBatchIdx(row)
			if err != nil {
				return nil, fmt.Errorf("parse batched audit pre-read row: %w", err)
			}
			oldRowsPerIndex[idx] = append(oldRowsPerIndex[idx], row)
		}
	}
	return oldRowsPerIndex, nil
}

// deparseTaggedRowSelect renders "SELECT <table> AS row_data, <tag> AS
// batch_idx FROM relation WHERE whereClause" — <table> selected bare
// (no alias) refers to the whole row as a single composite value, the
// same construct copyReadbackChunk's own "t AS row_data" relies on.
// captureRowsBeforeExecBatch UNION-ALLs one such branch per row so a
// caller can recover which paramSets index each returned row belongs to.
func deparseTaggedRowSelect(stmt auditableExecStmt, whereClause *pg_query.Node, tag int32) (string, error) {
	rowTarget := pg_query.MakeResTargetNodeWithNameAndVal("row_data",
		pg_query.MakeColumnRefNode([]*pg_query.Node{pg_query.MakeStrNode(stmt.Table)}, 0), 0)
	tagTarget := pg_query.MakeResTargetNodeWithNameAndVal("batch_idx", pg_query.MakeAConstIntNode(int64(tag), 0), 0)

	selectStmt := &pg_query.SelectStmt{
		TargetList:  []*pg_query.Node{rowTarget, tagTarget},
		FromClause:  []*pg_query.Node{{Node: &pg_query.Node_RangeVar{RangeVar: stmt.Relation}}},
		WhereClause: whereClause,
	}
	tree := &pg_query.ParseResult{
		Stmts: []*pg_query.RawStmt{
			{Stmt: &pg_query.Node{Node: &pg_query.Node_SelectStmt{SelectStmt: selectStmt}}},
		},
	}
	return pgquery.Deparse(tree)
}

// popBatchIdx removes and returns row's own "batch_idx" tag column
// (deparseTaggedRowSelect's own addition, not a real column of the
// audited table) — database/sql's generic driver.Value conversion for a
// plain integer literal can surface as any of Go's signed integer types
// depending on the driver, so this accepts the ones actually in use
// rather than assuming one.
func popBatchIdx(row map[string]any) (int, error) {
	v, ok := row["batch_idx"]
	delete(row, "batch_idx")
	if !ok {
		return 0, fmt.Errorf("missing batch_idx column")
	}
	switch n := v.(type) {
	case int64:
		return int(n), nil
	case int32:
		return int(n), nil
	case int:
		return n, nil
	default:
		return 0, fmt.Errorf("unexpected batch_idx type %T", v)
	}
}

// deparseSelectAll renders `SELECT * FROM relation [WHERE whereClause]`
// from already-parsed nodes lifted out of the module's own UPDATE/DELETE
// statement.
func deparseSelectAll(relation *pg_query.RangeVar, whereClause *pg_query.Node) (string, error) {
	star := pg_query.MakeResTargetNodeWithVal(
		pg_query.MakeColumnRefNode([]*pg_query.Node{pg_query.MakeAStarNode()}, 0), 0)

	selectStmt := &pg_query.SelectStmt{
		TargetList:  []*pg_query.Node{star},
		FromClause:  []*pg_query.Node{{Node: &pg_query.Node_RangeVar{RangeVar: relation}}},
		WhereClause: whereClause,
	}
	tree := &pg_query.ParseResult{
		Stmts: []*pg_query.RawStmt{
			{Stmt: &pg_query.Node{Node: &pg_query.Node_SelectStmt{SelectStmt: selectStmt}}},
		},
	}
	return pgquery.Deparse(tree)
}

// writeExecAuditEntries writes one audit_log row per row stmt affected —
// oldRows from captureRowsBeforeExec, newRows from the exec statement's
// own RETURNING output (empty for DELETE; for UPDATE, host.db.exec must
// request pkCol plus every non-excluded column via RETURNING regardless
// of the module's own opts.returning).
//
// Rows pair by pkCol value first — Postgres gives no ordering guarantee
// for either the pre-write SELECT or an UPDATE...RETURNING, so for a
// multi-row statement, pairing purely by array position risks matching
// row A's old_data against row B's new_data if the two statements ever
// scan in different orders. Only a row whose primary key itself changed
// (no value-match on either side) falls back to positional pairing
// against the equally-unmatched remainder, so an UPDATE that changes the
// primary key still produces one accurate before/after entry instead of
// a spurious delete+insert pair — this fallback set is normally at most
// one row (bulk primary-key mutation is not a realistic access pattern),
// where position is unambiguous regardless of scan order. A leftover,
// unpaired row on either side records with the other side NULL. Runs
// inside tx, so a later failure in the same write rolls these back too.
func writeExecAuditEntries(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, table string, stmt auditableExecStmt, pkCol string, excludeCols map[string]bool, oldRows, newRows []map[string]any) error {
	return insertAuditLogRows(ctx, tx, modCtx, table, stmt.Operation, excludeCols, pairAuditEntries(pkCol, oldRows, newRows))
}

// pairAuditEntries computes writeExecAuditEntries' own old/new pairing
// (see its doc comment above for the algorithm) without writing
// anything — split out so a caller pairing more than one statement's own
// oldRows/newRows (execBatchPipeline, host_db_exec_batch_fast.go) can
// accumulate every statement's own entries into one batched write while
// still pairing each statement's own rows in isolation, one statement at
// a time. Pairing across two different statements' own rows by primary
// key alone is unsafe whenever their WHERE clauses can target
// overlapping rows without binding identical parameter values — see
// captureRowsBeforeExecBatch's own doc comment for why that
// cross-statement collapse is a real, reachable case, not just
// theoretical.
func pairAuditEntries(pkCol string, oldRows, newRows []map[string]any) []auditLogEntry {
	type pair struct{ old, new map[string]any }
	var pairs []pair

	newByPK := make(map[any]map[string]any, len(newRows))
	for _, row := range newRows {
		newByPK[row[pkCol]] = row
	}
	consumed := make(map[any]bool, len(newRows))

	var unpairedOld []map[string]any
	for _, old := range oldRows {
		pk := old[pkCol]
		if newRow, ok := newByPK[pk]; ok && !consumed[pk] {
			pairs = append(pairs, pair{old: old, new: newRow})
			consumed[pk] = true
			continue
		}
		unpairedOld = append(unpairedOld, old)
	}

	var unpairedNew []map[string]any
	for _, row := range newRows {
		if !consumed[row[pkCol]] {
			unpairedNew = append(unpairedNew, row)
		}
	}

	for i := range max(len(unpairedOld), len(unpairedNew)) {
		var p pair
		if i < len(unpairedOld) {
			p.old = unpairedOld[i]
		}
		if i < len(unpairedNew) {
			p.new = unpairedNew[i]
		}
		pairs = append(pairs, p)
	}

	entries := make([]auditLogEntry, len(pairs))
	for i, p := range pairs {
		recordID := p.old[pkCol]
		if p.new != nil {
			recordID = p.new[pkCol]
		}
		entries[i] = auditLogEntry{RecordID: recordID, OldData: p.old, NewData: p.new}
	}
	return entries
}
