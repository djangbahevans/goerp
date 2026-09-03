package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// host.db.migration_ddl (host-abi-reference.md §5 "host.db.migration_ddl",
// migration-guide.md §4 "Dropping a column or table") is the explicit-
// consent escape hatch for the two DDL classes ordinary schema sync always
// blocks and never applies automatically (migration-guide.md §1 "The
// safety boundary"): dropping a column and dropping a table. Unlike
// schema sync's own DDL apply path (internal/engine/schema/apply.go), it
// runs a single, already-consented-to statement directly — no Atlas
// diffing, no NOT VALID/validate-later staging.
//
// A caller supplies structured {op, table, column}, not raw SQL — so this
// file builds the DDL itself from separately-validated identifiers rather
// than parsing/allowlisting a caller-supplied statement the way
// host.db.query/host.db.exec do (host_db_query.go's requireSelectOnly,
// host_db_exec.go's parseExecStmt): there is no SQL class to allowlist
// when the caller never sends SQL text at all.
//
// Ownership is checked against the caller's *currently declared* models
// only (ModuleContext.OwnedModels/ExtendsModels, resolved the same way
// host_db_exec_etag.go's resolveEtagTable resolves a bare table name) —
// deliberately, not against any broader table-ownership history. A table
// whose model has already been removed from the caller's own declaration
// (the shape migration-guide.md §4's own dropOldOrdersTable example
// describes) cannot be verified this way and is rejected: no persistent
// table→module ownership record exists anywhere in the engine to fall
// back on, and a deny-list check ("allow unless some *other* module
// currently owns it") would let any module drop any table nobody
// currently declares, which is a materially weaker guarantee than "the
// calling module actually declares ownership of" for a statement this
// destructive. DropTable is therefore only usable, today, against a table
// whose model the caller still declares (e.g. dropped in the same version
// as a data migration that first empties it) — extending it to a
// genuinely orphaned table needs a real ownership-history mechanism,
// deliberately left for later.

const (
	migrationDDLOpDropColumn = "drop_column"
	migrationDDLOpDropTable  = "drop_table"
)

// migrationDDLIdentifierRe is the same bare-identifier shape
// returningColumnRe (host_db_exec.go) validates opts.returning against —
// applied here to table/column so the DDL text below can be built by
// direct string concatenation with no SQL-injection risk through either
// field.
var migrationDDLIdentifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type dbMigrationDDLInput struct {
	Op     string `msgpack:"op"`
	Table  string `msgpack:"table"`
	Column string `msgpack:"column,omitempty"`
}

type dbMigrationDDLOutput struct {
	DurationMs float64 `msgpack:"duration_ms"`
}

func makeDBMigrationDDL(r *Runtime, primary *sql.DB) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapDBMigrationDDL) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("db.migration_ddl"))
		}
		if !modCtx.IsDataMigrationJob {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    abi.ErrCodeMigrationDDLNotInContext,
				Message: "host.db.migration_ddl may only be called from inside a data migration handler",
			})
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input dbMigrationDDLInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		output, hostErr := DBMigrationDDL(ctx, primary, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, output)
	}
}

// DBMigrationDDL implements host.db.migration_ddl's business logic —
// identifier/ownership validation (buildMigrationDDL) and executing the
// resulting statement — separated from makeDBMigrationDDL's own
// capability/IsDataMigrationJob gating and ABI marshaling so it's testable
// directly, matching DBExec's own split (host_db_exec.go).
func DBMigrationDDL(ctx context.Context, primary *sql.DB, modCtx *ModuleContext, input dbMigrationDDLInput) (dbMigrationDDLOutput, *abi.HostError) {
	sqlText, hostErr := buildMigrationDDL(modCtx, input)
	if hostErr != nil {
		return dbMigrationDDLOutput{}, hostErr
	}

	qCtx, cancel := context.WithTimeout(ctx, defaultExecTimeout)
	defer cancel()

	tx, err := primary.BeginTx(qCtx, nil)
	if err != nil {
		return dbMigrationDDLOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if err := applyTenantScope(qCtx, tx, modCtx); err != nil {
		_ = tx.Rollback()
		return dbMigrationDDLOutput{}, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	start := time.Now()
	if _, execErr := tx.ExecContext(qCtx, sqlText); execErr != nil {
		_ = tx.Rollback()
		return dbMigrationDDLOutput{}, translateMigrationDDLError(execErr)
	}
	if err := tx.Commit(); err != nil {
		return dbMigrationDDLOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}

	return dbMigrationDDLOutput{DurationMs: float64(time.Since(start).Microseconds()) / 1000}, nil
}

// buildMigrationDDL validates input — the table (and, for a column drop,
// the column) must belong to a model modCtx's own module currently owns
// or extends — and returns the exact DDL statement to run. See this
// file's own doc comment for why a since-removed model's table is
// rejected rather than allowed.
func buildMigrationDDL(modCtx *ModuleContext, input dbMigrationDDLInput) (string, *abi.HostError) {
	if !migrationDDLIdentifierRe.MatchString(input.Table) {
		return "", &abi.HostError{Code: abi.ErrCodeMigrationDDLError, Message: fmt.Sprintf("table %q is not a valid identifier", input.Table)}
	}

	decl, ok := resolveOwnedMigrationTable(modCtx, input.Table)
	if !ok {
		return "", &abi.HostError{Code: abi.ErrCodeMigrationDDLNotOwned, Message: fmt.Sprintf("table %q is not owned or extended by module %q", input.Table, modCtx.ModuleName)}
	}

	switch input.Op {
	case migrationDDLOpDropColumn:
		if !migrationDDLIdentifierRe.MatchString(input.Column) {
			return "", &abi.HostError{Code: abi.ErrCodeMigrationDDLError, Message: fmt.Sprintf("column %q is not a valid identifier", input.Column)}
		}
		if !hasField(decl, input.Column) {
			return "", &abi.HostError{Code: abi.ErrCodeMigrationDDLTargetNotFound, Message: fmt.Sprintf("column %q does not exist on %q", input.Column, input.Table)}
		}
		return "ALTER TABLE " + quoteIdent(input.Table) + " DROP COLUMN " + quoteIdent(input.Column), nil
	case migrationDDLOpDropTable:
		return "DROP TABLE " + quoteIdent(input.Table), nil
	default:
		return "", &abi.HostError{Code: abi.ErrCodeMigrationDDLError, Message: fmt.Sprintf("unknown op %q", input.Op)}
	}
}

// resolveOwnedMigrationTable resolves table against modCtx's own
// currently declared models, requiring the match to be a member of
// modCtx's OwnedModels or ExtendsModels — the same ownership
// relationship internal/engine/schema/session.go's SchemaSyncSession
// exposes for ordinary schema sync, applied here to a single explicit-
// consent DDL statement instead of a full diff/apply pass.
func resolveOwnedMigrationTable(modCtx *ModuleContext, table string) (model.ModelDeclaration, bool) {
	for _, decl := range modCtx.ModelDecls() {
		if tableNameForORM(decl) != table {
			continue
		}
		if migrationDDLOwnsModel(modCtx, decl.Name) {
			return decl, true
		}
		return model.ModelDeclaration{}, false
	}
	return model.ModelDeclaration{}, false
}

func migrationDDLOwnsModel(modCtx *ModuleContext, modelName string) bool {
	return slices.Contains(modCtx.OwnedModels(), modelName) || slices.Contains(modCtx.ExtendsModels(), modelName)
}

// quoteIdent double-quotes name for use as a Postgres identifier. Only
// ever called on a string already validated against
// migrationDDLIdentifierRe (bare [a-zA-Z_][a-zA-Z0-9_]* — no quote
// characters possible), so no escaping of embedded quotes is needed.
func quoteIdent(name string) string {
	return `"` + name + `"`
}

// translateMigrationDDLError maps a Postgres DDL failure to the ABI error
// code host-abi-reference.md documents for host.db.migration_ddl.
// undefined_column/undefined_table map to db.migration_ddl_target_not_found
// — buildMigrationDDL already checks the column against the caller's own
// declared model above, but the table's live catalog state is never
// re-checked before executing, only its declared-model membership, so a
// table dropped by a concurrent statement between validation and
// execution still surfaces this way. Everything else stays under the
// generic db.migration_ddl_error, carrying its own SQLSTATE.
func translateMigrationDDLError(err error) *abi.HostError {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "42703", "42P01": // undefined_column, undefined_table
			return &abi.HostError{Code: abi.ErrCodeMigrationDDLTargetNotFound, Message: pgErr.Message}
		default:
			return &abi.HostError{Code: abi.ErrCodeMigrationDDLError, Message: pgErr.Message, Details: map[string]any{"sqlstate": pgErr.Code}}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &abi.HostError{Code: abi.ErrCodeDBTimeout, Message: "migration DDL exceeded its timeout", Retry: true}
	}
	return &abi.HostError{Code: abi.ErrCodeMigrationDDLError, Message: err.Error()}
}
