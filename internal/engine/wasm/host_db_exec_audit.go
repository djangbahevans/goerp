package wasm

import (
	"context"
	"database/sql"
	"fmt"

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

// captureRowsBeforeExecBatch is captureRowsBeforeExec's own batched form:
// one SELECT for the whole batch (every row's own WHERE-clause target
// OR'd together) instead of one per row. Safe because
// pipelineHasDuplicateAuditTargets already excludes any batch whose rows
// could target the same primary key twice, so the combined result set is
// unambiguous regardless of statement order — the same guarantee
// execBatchPipeline's own eligibility check (host_db_exec_batch_fast.go)
// already relies on for pairing old/new rows by primary key after the
// fact. Chunked at maxAuditBatchChunkParams total bound parameters
// (host_orm_write.go), tracked per row via len(paramSets[i]) — a
// deliberately conservative proxy for a row's own true referenced-param
// count (renumberParamsFrom's own dedup can only make the real count
// smaller), safe to overestimate since it only ever makes a chunk
// somewhat smaller than the limit allows, never over it.
func captureRowsBeforeExecBatch(ctx context.Context, tx *sql.Tx, stmt auditableExecStmt, paramSets [][]any) ([]map[string]any, error) {
	if len(paramSets) == 0 {
		return nil, nil
	}
	if len(paramSets) == 1 {
		return captureRowsBeforeExec(ctx, tx, stmt, paramSets[0])
	}

	var allRows []map[string]any
	for i := 0; i < len(paramSets); {
		var nodes []*pg_query.Node
		var chunkParams []any
		weight := 0
		for i < len(paramSets) {
			rowWeight := len(paramSets[i])
			if len(nodes) > 0 && weight+rowWeight > maxAuditBatchChunkParams {
				break
			}
			node, rowParams, err := renumberParamsFrom(stmt.WhereClause, paramSets[i], int32(len(chunkParams)))
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, node)
			chunkParams = append(chunkParams, rowParams...)
			weight += rowWeight
			i++
		}

		combined := nodes[0]
		if len(nodes) > 1 {
			combined = pg_query.MakeBoolExprNode(pg_query.BoolExprType_OR_EXPR, nodes, 0)
		}
		selectSQL, err := deparseSelectAll(stmt.Relation, combined)
		if err != nil {
			return nil, fmt.Errorf("build batched audit pre-read query: %w", err)
		}
		rows, err := tx.QueryContext(ctx, selectSQL, chunkParams...)
		if err != nil {
			return nil, fmt.Errorf("read rows before audited exec batch: %w", err)
		}
		chunkRows, err := scanRowsToMaps(rows)
		if err != nil {
			return nil, err
		}
		allRows = append(allRows, chunkRows...)
	}
	return allRows, nil
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
	return insertAuditLogRows(ctx, tx, modCtx, table, stmt.Operation, excludeCols, entries)
}
