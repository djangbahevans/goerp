package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// Migration DDL accepts structured drop operations and shares schema sync's advisory lock
// to prevent concurrent catalog changes. Ownership requires a currently declared owned or
// extended model; a removed table cannot be verified. DropColumn permits a field removed
// from the current declaration.

func makeDBMigrationDDL(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapDBMigrationDDL) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("db.migration_ddl"))
		}
		if !modCtx.IsDataMigrationJob {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeMigrationDDLNotInContext,
				Message: "host.db.migration_ddl may only be called from inside a data migration handler",
			})
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.DBMigrationDDLInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		schemaSyncDB := r.schemaSyncDB.Load()
		if schemaSyncDB == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "no schema-sync database is configured"})
		}
		output, hostErr := DBMigrationDDL(ctx, schemaSyncDB, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, output)
	}
}

// DBMigrationDDL validates identifiers and model ownership, then executes a drop under
// schema sync's advisory lock. schemaSyncDB must use the role that owns tenant tables.
func DBMigrationDDL(ctx context.Context, schemaSyncDB *sql.DB, modCtx *ModuleContext, input abiv1.DBMigrationDDLInput) (abiv1.DBMigrationDDLOutput, *abiv1.HostError) {
	sqlText, hostErr := buildMigrationDDL(modCtx, input)
	if hostErr != nil {
		return abiv1.DBMigrationDDLOutput{}, hostErr
	}

	qCtx, cancel := context.WithTimeout(ctx, defaultExecTimeout)
	defer cancel()

	tx, cleanup, hostErr := beginMigrationDDLTx(qCtx, schemaSyncDB, modCtx)
	if hostErr != nil {
		return abiv1.DBMigrationDDLOutput{}, hostErr
	}
	defer cleanup()

	start := time.Now()
	if _, execErr := tx.ExecContext(qCtx, sqlText); execErr != nil {
		_ = tx.Rollback()
		return abiv1.DBMigrationDDLOutput{}, translateMigrationDDLError(execErr)
	}
	if err := tx.Commit(); err != nil {
		return abiv1.DBMigrationDDLOutput{}, &abiv1.HostError{Code: abiv1.ErrCodeCommitFailed, Message: err.Error()}
	}

	return abiv1.DBMigrationDDLOutput{DurationMs: float64(time.Since(start).Microseconds()) / 1000}, nil
}

// beginMigrationDDLTx takes the schema-sync tenant/module lock before scoped DDL. The
// caller must defer non-nil cleanup exactly once to release the lock and connection.
func beginMigrationDDLTx(ctx context.Context, schemaSyncDB *sql.DB, modCtx *ModuleContext) (tx *sql.Tx, cleanup func(), hostErr *abiv1.HostError) {
	conn, err := schemaSyncDB.Conn(ctx)
	if err != nil {
		return nil, nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	lockModule := modCtx.ModuleName
	if len(modCtx.OwnedModels()) == 0 && len(modCtx.ExtendsModels()) > 0 {
		lockModule, _, _ = strings.Cut(modCtx.ExtendsModels()[0], ".")
	}
	lockA, lockB := migrationDDLAdvisoryLockKeys(modCtx.TenantSlug, lockModule)
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1, $2)", lockA, lockB); err != nil {
		_ = conn.Close()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, &abiv1.HostError{Code: abiv1.ErrCodeDBTimeout, Message: "timed out waiting for the schema sync lock (a sync is in progress for this module/tenant)", Retry: true}
		}
		return nil, nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	cleanup = func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1, $2)", lockA, lockB)
		_ = conn.Close()
	}

	newTx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		cleanup()
		return nil, nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	if err := applyTenantScope(ctx, newTx, modCtx); err != nil {
		_ = newTx.Rollback()
		cleanup()
		return nil, nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}

	return newTx, cleanup, nil
}

// Lock keys are duplicated to avoid a schema/wasm test import cycle; a cross-package test
// verifies that both implementations agree.
func migrationDDLAdvisoryLockKeys(tenantSlug, moduleName string) (int32, int32) {
	h := fnv.New32a()
	h.Write([]byte(tenantSlug))
	a := int32(h.Sum32())
	h.Reset()
	h.Write([]byte(moduleName))
	b := int32(h.Sum32())
	return a, b
}

func buildMigrationDDL(modCtx *ModuleContext, input abiv1.DBMigrationDDLInput) (string, *abiv1.HostError) {
	if !returningColumnRe.MatchString(input.Table) {
		return "", &abiv1.HostError{Code: abiv1.ErrCodeMigrationDDLError, Message: fmt.Sprintf("table %q is not a valid identifier", input.Table)}
	}
	if !migrationDDLTableOwned(modCtx, input.Table) {
		return "", &abiv1.HostError{Code: abiv1.ErrCodeMigrationDDLNotOwned, Message: fmt.Sprintf("table %q is not owned or extended by module %q", input.Table, modCtx.ModuleName)}
	}

	switch input.Op {
	case abiv1.DBMigrationDDLOpDropColumn:
		if !returningColumnRe.MatchString(input.Column) {
			return "", &abiv1.HostError{Code: abiv1.ErrCodeMigrationDDLError, Message: fmt.Sprintf("column %q is not a valid identifier", input.Column)}
		}
		return "ALTER TABLE " + quoteIdentORM(input.Table) + " DROP COLUMN " + quoteIdentORM(input.Column), nil
	case abiv1.DBMigrationDDLOpDropTable:
		return "DROP TABLE " + quoteIdentORM(input.Table), nil
	default:
		return "", &abiv1.HostError{Code: abiv1.ErrCodeMigrationDDLError, Message: fmt.Sprintf("unknown op %q", input.Op)}
	}
}

// Migration DDL requires a matching declared physical table and its qualified name in
// OwnedModels or ExtendsModels.
func migrationDDLTableOwned(modCtx *ModuleContext, table string) bool {
	for _, decl := range modCtx.ModelDecls() {
		if modeltable.Name(decl) != table {
			continue
		}
		qualified := decl.QualifiedName(modCtx.ModuleName)
		return slices.Contains(modCtx.OwnedModels(), qualified) || slices.Contains(modCtx.ExtendsModels(), qualified)
	}
	for _, qualified := range modCtx.ExtendsModels() {
		if decl, ok := resolveAnyModel(modCtx, qualified); ok && modeltable.Name(decl) == table {
			return true
		}
	}
	return false
}

// Missing table or column errors become db.migration_ddl_target_not_found; other DDL
// errors retain their SQLSTATE under db.migration_ddl_error.
func translateMigrationDDLError(err error) *abiv1.HostError {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case "42703", "42P01": // undefined_column, undefined_table
			return &abiv1.HostError{Code: abiv1.ErrCodeMigrationDDLTargetNotFound, Message: pgErr.Message}
		default:
			return &abiv1.HostError{Code: abiv1.ErrCodeMigrationDDLError, Message: pgErr.Message, Details: map[string]any{"sqlstate": pgErr.Code}}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &abiv1.HostError{Code: abiv1.ErrCodeDBTimeout, Message: "migration DDL exceeded its timeout", Retry: true}
	}
	return &abiv1.HostError{Code: abiv1.ErrCodeMigrationDDLError, Message: err.Error()}
}
