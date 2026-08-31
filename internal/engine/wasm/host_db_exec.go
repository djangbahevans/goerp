package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/dbscope"
	"github.com/jackc/pgx/v5/pgconn"
	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
	pgquery "github.com/wasilibs/go-pgquery"
)

// host.db.exec (host-abi-reference.md §5 "host.db.exec"): parameterized
// INSERT/UPDATE/DELETE, with the etag (host_db_exec_etag.go) and audit
// (host_db_exec_audit.go) mechanisms wired into its own execution path,
// per goerp#460's own "why this is blocked" note — both were built ahead
// of this ticket specifically so they didn't need retrofitting in here.

const defaultExecTimeout = defaultQueryTimeout

// returningColumnRe matches a single bare column identifier — the only
// shape opts.returning accepts. No "*": host.db.exec's own ABI output
// has no column_names field (unlike host.db.query's), so there is no way
// to communicate a wildcard's resulting column order back to a caller
// that only gets positional any[][] rows.
var returningColumnRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type dbExecOpts struct {
	TimeoutMs  int64  `msgpack:"timeout_ms"`
	Returning  string `msgpack:"returning"`
	SkipAudit  bool   `msgpack:"skip_audit"`
	SkipEtag   bool   `msgpack:"skip_etag"`
	ExpectRows bool   `msgpack:"expect_rows"`
}

type dbExecInput struct {
	SQL    string     `msgpack:"sql"`
	Params []any      `msgpack:"params"`
	TxID   string     `msgpack:"tx_id"`
	Opts   dbExecOpts `msgpack:"opts"`
}

type dbExecOutput struct {
	RowsAffected int     `msgpack:"rows_affected"`
	Returning    [][]any `msgpack:"returning,omitempty"`
	DurationMs   float64 `msgpack:"duration_ms"`
}

func makeDBExec(r *Runtime, primary *sql.DB) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapDBWrite) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("db.write"))
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input dbExecInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		output, hostErr := DBExec(ctx, primary, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, output)
	}
}

// execStmt is what DBExec needs from a parsed INSERT, UPDATE, or DELETE
// statement — broader than host_db_exec_audit.go's auditableExecStmt,
// which only ever needs UPDATE/DELETE for its own before-row capture.
type execStmt struct {
	Operation     string // "INSERT", "UPDATE", or "DELETE"
	Relation      *pg_query.RangeVar
	WhereClause   *pg_query.Node   // nil for INSERT
	ReturningList []*pg_query.Node // must be empty as parsed — see DBExec
	stmtNode      *pg_query.Node   // the InsertStmt/UpdateStmt/DeleteStmt node itself, for injecting a RETURNING list before deparse
}

// parseExecStmt extracts stmt from tree's single statement, or an error
// for anything else — DDL included, rejected by omission the same way
// requireSelectOnly (host_db_query.go) allowlists SELECT rather than
// blocklisting DDL keywords.
func parseExecStmt(tree *pg_query.ParseResult) (execStmt, error) {
	stmts := tree.GetStmts()
	if len(stmts) != 1 {
		return execStmt{}, fmt.Errorf("host.db.exec accepts exactly one statement")
	}
	node := stmts[0].GetStmt()
	switch n := node.GetNode().(type) {
	case *pg_query.Node_InsertStmt:
		return execStmt{Operation: "INSERT", Relation: n.InsertStmt.GetRelation(), ReturningList: n.InsertStmt.GetReturningList(), stmtNode: node}, nil
	case *pg_query.Node_UpdateStmt:
		return execStmt{Operation: "UPDATE", Relation: n.UpdateStmt.GetRelation(), WhereClause: n.UpdateStmt.GetWhereClause(), ReturningList: n.UpdateStmt.GetReturningList(), stmtNode: node}, nil
	case *pg_query.Node_DeleteStmt:
		return execStmt{Operation: "DELETE", Relation: n.DeleteStmt.GetRelation(), WhereClause: n.DeleteStmt.GetWhereClause(), ReturningList: n.DeleteStmt.GetReturningList(), stmtNode: node}, nil
	default:
		return execStmt{}, fmt.Errorf("host.db.exec only permits INSERT, UPDATE, or DELETE statements — schema changes are handled exclusively by the schema sync engine, and reads go through host.db.query")
	}
}

// setReturningList assigns list as stmtNode's own RETURNING clause,
// mutating the parsed tree in place so a later Deparse of the same tree
// includes it.
func setReturningList(stmtNode *pg_query.Node, list []*pg_query.Node) {
	switch n := stmtNode.GetNode().(type) {
	case *pg_query.Node_InsertStmt:
		n.InsertStmt.ReturningList = list
	case *pg_query.Node_UpdateStmt:
		n.UpdateStmt.ReturningList = list
	case *pg_query.Node_DeleteStmt:
		n.DeleteStmt.ReturningList = list
	}
}

// parseReturningColumns validates opts.returning as a comma-separated
// list of bare column names (see returningColumnRe's own doc comment for
// why no "*").
func parseReturningColumns(returning string) ([]string, error) {
	if returning == "" {
		return nil, nil
	}
	parts := strings.Split(returning, ",")
	cols := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !returningColumnRe.MatchString(p) {
			return nil, fmt.Errorf("opts.returning: %q is not a valid column name", p)
		}
		cols = append(cols, p)
	}
	return cols, nil
}

// returningAllResTarget builds a `RETURNING *` target list — used
// internally whenever DBExec needs any row data back (the module's own
// opts.returning, the audit mechanism's new-row capture, or both), never
// exposed to the module directly: scanRowsToMaps' column-name-keyed
// result lets projectReturning and the audit functions each pick out
// only the columns they need afterward, without DBExec having to
// pre-compute which specific columns either side requires.
func returningAllResTarget() []*pg_query.Node {
	return []*pg_query.Node{
		pg_query.MakeResTargetNodeWithVal(
			pg_query.MakeColumnRefNode([]*pg_query.Node{pg_query.MakeAStarNode()}, 0), 0),
	}
}

// validateRequestedColumns errors if any of requested doesn't appear in
// available (the RETURNING * result's own real column set) — otherwise
// a mistyped or nonexistent opts.returning column would silently project
// to nil via projectReturning's plain map lookup, indistinguishable from
// a column whose real value happens to be NULL.
func validateRequestedColumns(requested, available []string) error {
	availableSet := make(map[string]bool, len(available))
	for _, c := range available {
		availableSet[c] = true
	}
	for _, c := range requested {
		if !availableSet[c] {
			return fmt.Errorf("opts.returning: column %q does not exist", c)
		}
	}
	return nil
}

// projectReturning reprojects rows (column-name-keyed, from the
// internal RETURNING * this file always executes when any returning is
// needed) onto cols, in cols' own order — the shape host.db.exec's own
// ABI output promises: positional any[][] matching opts.returning's
// order exactly.
func projectReturning(rows []map[string]any, cols []string) [][]any {
	out := make([][]any, len(rows))
	for i, row := range rows {
		vals := make([]any, len(cols))
		for j, c := range cols {
			vals[j] = row[c]
		}
		out[i] = vals
	}
	return out
}

// preparedExec is host.db.exec's own parsed, validated, deparsed
// statement plus everything about it that doesn't vary per parameter
// set — computed once by prepareExec and reused by execRow however many
// times a caller runs it (once for DBExec, once per parameter set for
// DBExecBatch, host_db_exec_batch.go).
type preparedExec struct {
	stmt          execStmt
	table         string
	finalSQL      string
	needReturning bool
	requestedCols []string
	audited       bool
	pkCol         string
	excludeCols   map[string]bool
	hasEtagCol    bool
	hadEtagCheck  bool
}

// prepareExec parses sqlText as a single INSERT/UPDATE/DELETE statement,
// validates it the way host.db.exec's own ABI contract requires (no
// qualified table refs, no caller-supplied RETURNING), and resolves the
// audit/etag mechanisms against modCtx's declared models — everything
// execRow needs that stays constant across every parameter set a single
// statement is run against.
func prepareExec(sqlText string, opts dbExecOpts, modCtx *ModuleContext) (preparedExec, *abi.HostError) {
	tree, err := pgquery.Parse(sqlText)
	if err != nil {
		return preparedExec{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
	}
	stmt, err := parseExecStmt(tree)
	if err != nil {
		return preparedExec{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
	}
	if len(stmt.ReturningList) > 0 {
		return preparedExec{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: "host.db.exec statements must not include their own RETURNING clause — use opts.returning instead"}
	}
	if err := dbscope.ValidateTreeNoQualifiedTableRefs(tree); err != nil {
		return preparedExec{}, &abi.HostError{Code: abi.ErrCodeTableAccessDenied, Message: err.Error()}
	}

	requestedCols, err := parseReturningColumns(opts.Returning)
	if err != nil {
		return preparedExec{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
	}

	table := stmt.Relation.GetRelname()

	var (
		pkCol        string
		excludeCols  map[string]bool
		audited      bool
		hasEtagCol   bool
		hadEtagCheck bool
	)
	if !opts.SkipAudit {
		pkCol, excludeCols, audited = resolveAuditedExecTable(modCtx, table)
	}
	if stmt.Operation == "UPDATE" && !opts.SkipEtag {
		if _, ok := resolveEtagTable(modCtx, table); ok {
			hasEtagCol = true
			hadEtagCheck = whereClauseHasEtagCheck(stmt.WhereClause, stmt.Relation)
		}
	}

	needReturning := requestedCols != nil || (audited && stmt.Operation != "DELETE")
	if needReturning {
		setReturningList(stmt.stmtNode, returningAllResTarget())
	}
	finalSQL, err := pgquery.Deparse(tree)
	if err != nil {
		return preparedExec{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
	}

	return preparedExec{
		stmt: stmt, table: table, finalSQL: finalSQL, needReturning: needReturning,
		requestedCols: requestedCols, audited: audited, pkCol: pkCol, excludeCols: excludeCols,
		hasEtagCol: hasEtagCol, hadEtagCheck: hadEtagCheck,
	}, nil
}

// beginOrBorrowExecTx returns the *sql.Tx a statement should run in — the
// caller's own transaction if txID names one already registered via
// host.db.begin, otherwise a freshly-opened, tenant-scoped one. finish
// commits or rolls back a freshly-opened transaction; for a borrowed one
// it's a no-op, since that transaction is owned by whoever called
// host.db.begin, not by this call.
func beginOrBorrowExecTx(qCtx context.Context, primary *sql.DB, modCtx *ModuleContext, txID string) (tx *sql.Tx, finish func(error) error, hostErr *abi.HostError) {
	if txID != "" {
		borrowed, ok := modCtx.Transaction(txID)
		if !ok {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeTransactionNotFound, Message: "transaction ID does not exist or has expired"}
		}
		return borrowed, func(error) error { return nil }, nil
	}

	newTx, err := primary.BeginTx(qCtx, nil)
	if err != nil {
		return nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if err := applyTenantScope(qCtx, newTx, modCtx); err != nil {
		_ = newTx.Rollback()
		return nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	finish = func(callErr error) error {
		if callErr != nil {
			return newTx.Rollback()
		}
		return newTx.Commit()
	}
	return newTx, finish, nil
}

// execRowResult is what a single execRow call produces on success.
// Duration covers only the statement's own execution (the
// QueryContext/ExecContext call) — not audit-log writes — matching what
// host.db.exec's own duration_ms has always measured.
type execRowResult struct {
	RowsAffected int64
	Returning    [][]any
	Duration     time.Duration
}

// execRow runs p — a statement prepareExec already parsed, validated, and
// deparsed — once against tx with params, applying its etag/audit
// mechanisms exactly as a single host.db.exec call would. This is the
// unit both DBExec (once) and DBExecBatch (host_db_exec_batch.go, once
// per parameter set within a single shared transaction) run — the reuse
// goerp#461's own scope calls for, rather than either one reimplementing
// RETURNING construction, constraint-violation translation, or the
// etag/audit mechanisms itself.
//
// ctx must already carry whatever deadline this row's execution should
// run under — execRow doesn't derive one itself. DBExec bounds its one
// call with a single timeout shared with its own transaction's BeginTx;
// DBExecBatch (host_db_exec_batch.go) instead gives each parameter set
// its own fresh per-row timeout window, independent of the batch
// transaction's own lifetime.
func execRow(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, p preparedExec, params []any) (execRowResult, *abi.HostError) {
	var oldRows []map[string]any
	if p.audited && p.stmt.Operation != "INSERT" {
		var err error
		oldRows, err = captureRowsBeforeExec(ctx, tx, auditableExecStmt{
			Operation: p.stmt.Operation, Table: p.table, Relation: p.stmt.Relation, WhereClause: p.stmt.WhereClause,
		}, params)
		if err != nil {
			return execRowResult{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
		}
	}

	start := time.Now()
	var (
		newRows      []map[string]any
		rowsAffected int64
	)
	if p.needReturning {
		rows, execErr := tx.QueryContext(ctx, p.finalSQL, params...)
		if execErr != nil {
			return execRowResult{}, translateExecError(execErr)
		}
		// The internally-executed RETURNING * always includes every
		// column, so its own result set defines the table's real column
		// set — available even when zero rows come back, unlike scanning
		// each row's own map keys. Checked before scanning, since a
		// mistyped or nonexistent opts.returning column would otherwise
		// silently project to nil (indistinguishable from a real NULL
		// value) instead of a clear error.
		if p.requestedCols != nil {
			available, err := rows.Columns()
			if err != nil {
				_ = rows.Close()
				return execRowResult{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
			}
			if err := validateRequestedColumns(p.requestedCols, available); err != nil {
				_ = rows.Close()
				return execRowResult{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
			}
		}
		var err error
		newRows, err = scanRowsToMaps(rows)
		if err != nil {
			return execRowResult{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
		}
		rowsAffected = int64(len(newRows))
	} else {
		result, execErr := tx.ExecContext(ctx, p.finalSQL, params...)
		if execErr != nil {
			return execRowResult{}, translateExecError(execErr)
		}
		var err error
		rowsAffected, err = result.RowsAffected()
		if err != nil {
			return execRowResult{}, &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
		}
	}
	duration := time.Since(start)

	if p.hasEtagCol && isEtagMismatch(p.hadEtagCheck, rowsAffected) {
		return execRowResult{}, &abi.HostError{Code: abi.ErrCodeDBEtagMismatch, Message: "record has been modified since it was last read"}
	}

	if p.audited {
		if auditErr := writeAuditForExec(ctx, tx, modCtx, p.table, p.stmt, p.pkCol, p.excludeCols, oldRows, newRows); auditErr != nil {
			return execRowResult{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: auditErr.Error()}
		}
	}

	out := execRowResult{RowsAffected: rowsAffected, Duration: duration}
	if p.requestedCols != nil {
		out.Returning = projectReturning(newRows, p.requestedCols)
	}
	return out, nil
}

// DBExec implements host.db.exec (host-abi-reference.md §5). primary is
// only used to open a new transaction when input.TxID is empty — every
// statement this function runs goes through a *sql.Tx either way, since
// host.db.exec's own etag/audit mechanisms need transactional
// consistency between the pre-write read and the write itself.
func DBExec(ctx context.Context, primary *sql.DB, modCtx *ModuleContext, input dbExecInput) (dbExecOutput, *abi.HostError) {
	p, hostErr := prepareExec(input.SQL, input.Opts, modCtx)
	if hostErr != nil {
		return dbExecOutput{}, hostErr
	}

	timeout := defaultExecTimeout
	if input.Opts.TimeoutMs > 0 {
		timeout = time.Duration(input.Opts.TimeoutMs) * time.Millisecond
	}
	qCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tx, finish, hostErr := beginOrBorrowExecTx(qCtx, primary, modCtx, input.TxID)
	if hostErr != nil {
		return dbExecOutput{}, hostErr
	}

	result, hostErr := execRow(qCtx, tx, modCtx, p, input.Params)
	if hostErr != nil {
		_ = finish(errors.New(hostErr.Message))
		return dbExecOutput{}, hostErr
	}

	if input.Opts.ExpectRows && result.RowsAffected == 0 {
		_ = finish(errors.New("no rows affected"))
		return dbExecOutput{}, &abi.HostError{Code: abi.ErrCodeNoRowsAffected, Message: "statement matched zero rows"}
	}

	if err := finish(nil); err != nil {
		return dbExecOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	if result.Duration > slowQueryThreshold {
		log.Warn().Str("module", modCtx.ModuleName).Str("sql", input.SQL).
			Dur("duration", result.Duration).Msg("host.db.exec: slow exec")
	}

	output := dbExecOutput{RowsAffected: int(result.RowsAffected), DurationMs: float64(result.Duration.Microseconds()) / 1000}
	if p.requestedCols != nil {
		output.Returning = result.Returning
	}
	return output, nil
}

// writeAuditForExec writes stmt's audit_log entries, dispatching by
// operation: INSERT has no "before" state to capture (writeAuditEntries'
// UPDATE/DELETE pairing logic doesn't apply), so it writes one row per
// newRows entry directly; UPDATE/DELETE reuse writeExecAuditEntries
// (host_db_exec_audit.go) exactly as the audit mechanism's own tests
// exercise it.
func writeAuditForExec(ctx context.Context, tx *sql.Tx, modCtx *ModuleContext, table string, stmt execStmt, pkCol string, excludeCols map[string]bool, oldRows, newRows []map[string]any) error {
	if stmt.Operation == "INSERT" {
		for _, row := range newRows {
			if err := insertAuditLogRow(ctx, tx, modCtx, table, row[pkCol], "INSERT", excludeCols, nil, row); err != nil {
				return err
			}
		}
		return nil
	}
	if stmt.Operation == "DELETE" {
		// newRows may be non-nil here if the module set opts.returning on
		// this DELETE — those rows reflect each row's last values before
		// removal, not "new" state. new_data must stay NULL regardless of
		// whether RETURNING was requested for the module's own purposes.
		newRows = nil
	}
	return writeExecAuditEntries(ctx, tx, modCtx, table, auditableExecStmt{
		Operation: stmt.Operation, Table: table, Relation: stmt.Relation, WhereClause: stmt.WhereClause,
	}, pkCol, excludeCols, oldRows, newRows)
}

// translateExecError maps a Postgres write failure to the ABI error code
// host-abi-reference.md documents for host.db.exec specifically — the
// "db." prefix, distinct from host.orm's own "orm."-prefixed codes
// translateWriteError (host_orm_write.go) returns for the same
// underlying Postgres errors, and with FK violation detail shaped as
// table+column (per the doc's own "structured: includes table and
// column") rather than translateWriteError's constraint-name-only shape.
func translateExecError(err error) *abi.HostError {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "23505": // unique_violation
			return &abi.HostError{Code: abi.ErrCodeDBUniqueViolation, Message: pgErr.Message, Details: map[string]any{"constraint": pgErr.ConstraintName, "sqlstate": pgErr.Code}}
		case "23503", "23001": // foreign_key_violation, restrict_violation
			return &abi.HostError{Code: abi.ErrCodeDBForeignKeyViolation, Message: pgErr.Message, Details: map[string]any{"table": pgErr.TableName, "column": fkViolationColumn(pgErr), "sqlstate": pgErr.Code}}
		default:
			// Every other Postgres error (deadlocks, check/not-null
			// violations, ...) stays under the generic db.exec_error
			// code, but still carries its own SQLSTATE — the only
			// structured signal a caller has to distinguish, say, a
			// deadlock (40P01, safe to retry) from anything else in
			// this bucket without parsing Message's own free text.
			return &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error(), Details: map[string]any{"sqlstate": pgErr.Code}}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &abi.HostError{Code: abi.ErrCodeDBTimeout, Message: "execution exceeded its timeout", Retry: true}
	}
	return &abi.HostError{Code: abi.ErrCodeExecError, Message: err.Error()}
}

// fkViolationColumnRe matches the column list Postgres's own FK-violation
// detail message always starts with — "Key (col1, col2)=(...) is not
// present in table ...". Postgres never populates PgError.ColumnName for
// this error class (unlike a NOT NULL violation, which does) — Detail is
// the only place the column name(s) exist in the structured error at
// all, confirmed against a real 23503 error, not assumed from the docs.
var fkViolationColumnRe = regexp.MustCompile(`^Key \(([^)]+)\)=`)

func fkViolationColumn(pgErr *pgconn.PgError) string {
	if pgErr.ColumnName != "" {
		return pgErr.ColumnName
	}
	if m := fkViolationColumnRe.FindStringSubmatch(pgErr.Detail); m != nil {
		return m[1]
	}
	return ""
}
