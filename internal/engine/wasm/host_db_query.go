package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/dbscope"
	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
	pgquery "github.com/wasilibs/go-pgquery"
)

// defaultQueryTimeout is host-abi-reference.md's own documented default
// for host.db.query's opts.timeout_ms.
const defaultQueryTimeout = 30 * time.Second

// maxQueryResultRows is host-abi-reference.md's own documented, fixed
// result-set cap for host.db.query — not configurable per-call or via
// env var, matching the doc's own "Maximum result set size: 50,000 rows"
// wording.
const maxQueryResultRows = 50_000

// slowQueryThreshold is host-abi-reference.md's own documented threshold
// for automatic slow-query logging.
const slowQueryThreshold = 1 * time.Second

// errResultTooLarge is scanRowsToSlices' sentinel for exceeding
// maxQueryResultRows — kept distinct from a bare scan/driver error so
// makeDBQuery can map it to db.result_too_large specifically rather than
// the generic db.query_error.
var errResultTooLarge = errors.New("result set exceeds the maximum of 50,000 rows")

type dbQueryOpts struct {
	TimeoutMs int64 `msgpack:"timeout_ms"`
	ReadOnly  bool  `msgpack:"read_only"`
}

type dbQueryInput struct {
	SQL    string      `msgpack:"sql"`
	Params []any       `msgpack:"params"`
	TxID   string      `msgpack:"tx_id"`
	Opts   dbQueryOpts `msgpack:"opts"`
}

type dbQueryOutput struct {
	Rows         [][]any  `msgpack:"rows"`
	ColumnNames  []string `msgpack:"column_names"`
	RowsAffected int      `msgpack:"rows_affected"`
	DurationMs   float64  `msgpack:"duration_ms"`
}

// makeDBQuery builds host.db.query (forceReplica false, opts.read_only
// still routes it to r's replica when set) or host.db.query_replica
// (forceReplica true, always routes to replica regardless of opts).
func makeDBQuery(r *Runtime, primary *sql.DB, forceReplica bool) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapDBRead) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("db.read"))
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input dbQueryInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		// One parse feeds both checks below, and both must run before
		// anything executes.
		tree, err := pgquery.Parse(input.SQL)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeQueryError, Message: err.Error()})
		}
		if err := requireSelectOnly(tree); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeQueryError, Message: err.Error()})
		}
		if err := dbscope.ValidateTreeNoQualifiedTableRefs(tree); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeTableAccessDenied, Message: err.Error()})
		}

		timeout := defaultQueryTimeout
		if input.Opts.TimeoutMs > 0 {
			timeout = time.Duration(input.Opts.TimeoutMs) * time.Millisecond
		}
		qCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		var (
			q      *sql.Tx
			finish func(error) error
		)
		if input.TxID != "" {
			// A borrowed transaction is already bound to whatever pool
			// host.db.begin opened it against — always primary, never a
			// replica — so host.db.query_replica's "always routes to
			// replica" guarantee can't be honored here; reject rather than
			// silently running on primary.
			if forceReplica {
				return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
					Code:    abi.ErrCodeReplicaUnavailable,
					Message: "host.db.query_replica cannot run inside an existing transaction, which is always bound to primary",
				})
			}
			tx, ok := modCtx.Transaction(input.TxID)
			if !ok {
				return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
					Code:    abi.ErrCodeTransactionNotFound,
					Message: "transaction ID does not exist or has expired",
				})
			}
			q = tx
			// Owned by whoever called host.db.begin.
			finish = func(error) error { return nil }
		} else {
			target := primary
			if forceReplica || input.Opts.ReadOnly {
				replica := r.replicaDB.Load()
				if replica == nil {
					return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
						Code:    abi.ErrCodeReplicaUnavailable,
						Message: "no read replica is configured",
					})
				}
				target = replica
			}

			tx, err := target.BeginTx(qCtx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				return abi.EncodeHostError(ctx, m, allocate, queryHostError(err))
			}
			if err := applyTenantScope(qCtx, tx, modCtx); err != nil {
				_ = tx.Rollback()
				return abi.EncodeHostError(ctx, m, allocate, queryHostError(err))
			}
			q = tx
			finish = func(callErr error) error {
				if callErr != nil {
					return tx.Rollback()
				}
				return tx.Commit()
			}
		}

		start := time.Now()
		rows, err := q.QueryContext(qCtx, input.SQL, input.Params...)
		if err != nil {
			_ = finish(err)
			return abi.EncodeHostError(ctx, m, allocate, queryHostError(err))
		}

		cols, values, scanErr := scanRowsToSlices(rows, maxQueryResultRows)
		finishErr := finish(scanErr)
		duration := time.Since(start)

		if scanErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, queryHostError(scanErr))
		}
		if finishErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, queryHostError(finishErr))
		}

		if duration > slowQueryThreshold {
			// Never logs input.Params — only the parameterized SQL text
			// itself ("sanitized" per host-abi-reference.md: no literal
			// values, since a module is expected to use positional params
			// rather than string-interpolating them into input.SQL).
			log.Warn().Str("module", modCtx.ModuleName).Str("sql", input.SQL).
				Dur("duration", duration).Msg("host.db.query: slow query")
		}

		return abi.WriteToModule(ctx, m, allocate, dbQueryOutput{
			Rows:         values,
			ColumnNames:  cols,
			RowsAffected: 0,
			DurationMs:   float64(duration.Microseconds()) / 1000,
		})
	}
}

// requireSelectOnly allowlists *pg_query.Node_SelectStmt rather than
// blocklisting DDL node types: Postgres has more schema/permission-
// mutating statement kinds than CREATE/DROP/ALTER/TRUNCATE alone (RENAME,
// GRANT, CREATE INDEX, COMMENT ON, ...), and a blocklist only catches the
// ones it happens to enumerate. A SelectStmt with a non-nil IntoClause
// (SELECT ... INTO new_table) is rejected too — it creates a table
// despite the SELECT keyword.
func requireSelectOnly(tree *pg_query.ParseResult) error {
	for _, raw := range tree.GetStmts() {
		sel, ok := raw.GetStmt().GetNode().(*pg_query.Node_SelectStmt)
		if !ok {
			return fmt.Errorf("host.db.query only permits SELECT statements — schema changes are handled exclusively by the schema sync engine, and writes go through host.db.exec")
		}
		if sel.SelectStmt.GetIntoClause() != nil {
			return fmt.Errorf("SELECT ... INTO is not permitted — it creates a table, which host.db.query does not allow")
		}
	}
	return nil
}

// scanRowsToSlices reads every row of rows into an ordered []any per row
// (host.db.query's own "rows: any[][]" ABI shape — a positional array,
// not scanRowsToMaps' column-name-keyed map host.orm's read path uses),
// aborting with errResultTooLarge the moment maxRows would be exceeded
// rather than reading the rest of a possibly-much-larger result set first.
func scanRowsToSlices(rows *sql.Rows, maxRows int) (columns []string, values [][]any, err error) {
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	for rows.Next() {
		if len(values) >= maxRows {
			return nil, nil, errResultTooLarge
		}
		row, err := scanRowValues(rows, len(cols))
		if err != nil {
			return nil, nil, err
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return cols, values, nil
}

// scanRowValues scans the row rows.Next() just advanced to into a
// []any of length numCols — the one piece of scan mechanics
// scanRowsToSlices and host_orm.go's scanRowsToMaps share, despite
// building different output shapes (positional slice vs. column-keyed
// map) and different outer-loop behavior (a row cap here, none there)
// around it.
func scanRowValues(rows *sql.Rows, numCols int) ([]any, error) {
	values := make([]any, numCols)
	ptrs := make([]any, numCols)
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	return values, nil
}

// queryHostError maps err to the ABI error code host-abi-reference.md
// documents for it: db.result_too_large for errResultTooLarge,
// db.timeout for a context deadline (Retry: true — host-abi-reference.md
// §5 "host.db.commit" establishes the same Retry convention for a
// transient, retry-safe failure), db.query_error for everything else
// (SQL syntax errors, constraint violations, other Postgres errors).
func queryHostError(err error) *abi.HostError {
	switch {
	case errors.Is(err, errResultTooLarge):
		return &abi.HostError{Code: abi.ErrCodeResultTooLarge, Message: err.Error()}
	case errors.Is(err, context.DeadlineExceeded):
		return &abi.HostError{Code: abi.ErrCodeDBTimeout, Message: "query exceeded its timeout", Retry: true}
	default:
		return &abi.HostError{Code: abi.ErrCodeQueryError, Message: err.Error()}
	}
}
