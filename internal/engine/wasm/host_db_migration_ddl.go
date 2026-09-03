package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
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
// diffing, no NOT VALID/validate-later staging — but it still takes the
// same pg_advisory_lock BeginSync (internal/engine/schema/pool.go) holds
// for this (tenant, module) pair's whole DDL apply, so a direct drop can
// never interleave with a concurrent Atlas-driven sync for the same pair
// mutating the same catalog state out from under either one.
//
// A caller supplies structured {op, table, column}, not raw SQL — so this
// file builds the DDL itself from separately-validated identifiers rather
// than parsing/allowlisting a caller-supplied statement the way
// host.db.query/host.db.exec do (host_db_query.go's requireSelectOnly,
// host_db_exec.go's parseExecStmt): there is no SQL class to allowlist
// when the caller never sends SQL text at all.
//
// Ownership is checked against the caller's *currently declared* models
// only (ModuleContext.OwnedModels/ExtendsModels) — deliberately, not
// against any broader table-ownership history. A table whose model has
// already been removed from the caller's own declaration (the shape
// migration-guide.md §4's own dropOldOrdersTable example describes)
// cannot be verified this way and is rejected: no persistent
// table→module ownership record exists anywhere in the engine to fall
// back on, and a deny-list check ("allow unless some *other* module
// currently owns it") would let any module drop any table nobody
// currently declares, which is a materially weaker guarantee than "the
// calling module actually declares ownership of" for a statement this
// destructive. DropTable is therefore only usable, today, against a table
// whose model the caller still declares (e.g. dropped in the same version
// as a data migration that first empties it) — extending it to a
// genuinely orphaned table needs a real ownership-history mechanism,
// deliberately left for later. The same table-level check also doesn't
// consult *other* modules' ExtendsModels: a field_extension module's own
// columns on a table this caller legitimately owns are destroyed by
// DropTable with no separate signal to that other module — accepted for
// the same reason, not something this ownership check can see without
// that same history mechanism.
//
// DropColumn's own target, by contrast, only needs the table's model
// still owned/extended — the column itself does not need to still appear
// in the caller's current field declaration. migration-guide.md §3.11's
// own documented workflow ("Remove from the model declaration and add a
// handler") removes the Field() call in the very same change that adds
// the DropColumn handler, so by the time the handler runs,
// get_model_declarations() already reflects the column's removal —
// requiring it to still be declared would reject the documented workflow
// outright. A genuinely nonexistent column is instead caught by Postgres
// itself (translateMigrationDDLError's undefined_column case below).

const (
	migrationDDLOpDropColumn = "drop_column"
	migrationDDLOpDropTable  = "drop_table"
)

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
// resulting statement under the same advisory lock schema sync uses —
// separated from makeDBMigrationDDL's own capability/IsDataMigrationJob
// gating and ABI marshaling so it's testable directly, matching DBExec's
// own split (host_db_exec.go).
func DBMigrationDDL(ctx context.Context, primary *sql.DB, modCtx *ModuleContext, input dbMigrationDDLInput) (dbMigrationDDLOutput, *abi.HostError) {
	sqlText, hostErr := buildMigrationDDL(modCtx, input)
	if hostErr != nil {
		return dbMigrationDDLOutput{}, hostErr
	}

	qCtx, cancel := context.WithTimeout(ctx, defaultExecTimeout)
	defer cancel()

	tx, cleanup, hostErr := beginMigrationDDLTx(qCtx, primary, modCtx)
	if hostErr != nil {
		return dbMigrationDDLOutput{}, hostErr
	}
	defer cleanup()

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

// beginMigrationDDLTx acquires a *sql.Conn, takes the same pg_advisory_lock
// BeginSync itself takes for (modCtx.TenantSlug, modCtx.ModuleName) —
// serializing this direct DDL statement against a concurrent Atlas-driven
// schema sync for the identical pair — then opens a transaction on that
// same connection and applies tenant scope to it. cleanup unlocks and
// closes conn; the caller must defer it exactly once as soon as it's
// returned non-nil, regardless of how tx is later used.
func beginMigrationDDLTx(ctx context.Context, primary *sql.DB, modCtx *ModuleContext) (tx *sql.Tx, cleanup func(), hostErr *abi.HostError) {
	conn, err := primary.Conn(ctx)
	if err != nil {
		return nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	lockA, lockB := migrationDDLAdvisoryLockKeys(modCtx.TenantSlug, modCtx.ModuleName)
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1, $2)", lockA, lockB); err != nil {
		_ = conn.Close()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, &abi.HostError{Code: abi.ErrCodeDBTimeout, Message: "timed out waiting for the schema sync lock (a sync is in progress for this module/tenant)", Retry: true}
		}
		return nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	cleanup = func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1, $2)", lockA, lockB)
		_ = conn.Close()
	}

	newTx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		cleanup()
		return nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if err := applyTenantScope(ctx, newTx, modCtx); err != nil {
		_ = newTx.Rollback()
		cleanup()
		return nil, nil, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	return newTx, cleanup, nil
}

// migrationDDLAdvisoryLockKeys mirrors internal/engine/schema/pool.go's
// own AdvisoryLockKeys exactly — duplicated locally rather than imported,
// since internal/engine/schema's own test suite
// (enum_realmodule_test.go) imports this package (a real compiled-module
// integration test), and this package importing schema back would be a
// cycle (the same reason host_orm.go's tableNameForORM duplicates
// schema.TableNameFor instead of importing it).
// TestMigrationDDLAdvisoryLockKeys_MatchesSchemaPackage cross-checks this
// against schema.AdvisoryLockKeys directly (safe in a test file, since
// only schema's test files import wasm, not schema's production code).
func migrationDDLAdvisoryLockKeys(tenantSlug, moduleName string) (int32, int32) {
	h := fnv.New32a()
	h.Write([]byte(tenantSlug))
	a := int32(h.Sum32())
	h.Reset()
	h.Write([]byte(moduleName))
	b := int32(h.Sum32())
	return a, b
}

// buildMigrationDDL validates input — the table must belong to a model
// modCtx's own module currently owns or extends — and returns the exact
// DDL statement to run. See this file's own doc comment for why a
// since-removed model's table is rejected rather than allowed, and why
// DropColumn's own column argument isn't checked against the current
// declaration the same way.
func buildMigrationDDL(modCtx *ModuleContext, input dbMigrationDDLInput) (string, *abi.HostError) {
	if !returningColumnRe.MatchString(input.Table) {
		return "", &abi.HostError{Code: abi.ErrCodeMigrationDDLError, Message: fmt.Sprintf("table %q is not a valid identifier", input.Table)}
	}
	if !migrationDDLTableOwned(modCtx, input.Table) {
		return "", &abi.HostError{Code: abi.ErrCodeMigrationDDLNotOwned, Message: fmt.Sprintf("table %q is not owned or extended by module %q", input.Table, modCtx.ModuleName)}
	}

	switch input.Op {
	case migrationDDLOpDropColumn:
		if !returningColumnRe.MatchString(input.Column) {
			return "", &abi.HostError{Code: abi.ErrCodeMigrationDDLError, Message: fmt.Sprintf("column %q is not a valid identifier", input.Column)}
		}
		return "ALTER TABLE " + quoteIdentORM(input.Table) + " DROP COLUMN " + quoteIdentORM(input.Column), nil
	case migrationDDLOpDropTable:
		return "DROP TABLE " + quoteIdentORM(input.Table), nil
	default:
		return "", &abi.HostError{Code: abi.ErrCodeMigrationDDLError, Message: fmt.Sprintf("unknown op %q", input.Op)}
	}
}

// migrationDDLTableOwned reports whether table resolves (via
// tableNameForORM, the same bare-name mapping resolveEtagTable and
// resolveAuditedExecTable use) to one of modCtx's own currently declared
// models, *and* that model's fully-qualified "{module}.{resource}" name
// — the form manifest.SchemaConfig.OwnedModels/ExtendsModels always use,
// the same qualification resolveModel (host_orm.go) strips before
// comparing against a bare ModelDecls entry — is a member of
// modCtx.OwnedModels() or modCtx.ExtendsModels().
func migrationDDLTableOwned(modCtx *ModuleContext, table string) bool {
	for _, decl := range modCtx.ModelDecls() {
		if tableNameForORM(decl) != table {
			continue
		}
		qualified := modCtx.ModuleName + "." + decl.Name
		return slices.Contains(modCtx.OwnedModels(), qualified) || slices.Contains(modCtx.ExtendsModels(), qualified)
	}
	return false
}

// translateMigrationDDLError maps a Postgres DDL failure to the ABI error
// code host-abi-reference.md documents for host.db.migration_ddl.
// undefined_column/undefined_table map to db.migration_ddl_target_not_found
// — the only place a nonexistent DropColumn target is ever caught, per
// this file's own doc comment on why buildMigrationDDL doesn't pre-check
// the column against the caller's current declaration. Everything else
// stays under the generic db.migration_ddl_error, carrying its own
// SQLSTATE.
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
