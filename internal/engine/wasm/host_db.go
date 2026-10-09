package wasm

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

const transactionExpiry = 30 * time.Second

// registerHostDB attaches host.db.begin/commit/rollback to the runtime.
// It lives in the wasm package (not abi, where every other namespace is
// registered) because its closures need direct access to *sql.DB, the
// Runtime's instance registry, and the Runtime's TransactionLimiter — all
// wasm-package types abi cannot import without an import cycle (wasm
// already imports abi for CapabilitySet/HostError).
func registerHostDB(ctx context.Context, rt wazero.Runtime, r *Runtime, db *sql.DB) error {
	_, err := r.guardedHostModule(rt, "host.db").
		NewFunctionBuilder().WithFunc(makeDBBegin(r, db)).Export("begin").
		NewFunctionBuilder().WithFunc(makeDBCommit(r)).Export("commit").
		NewFunctionBuilder().WithFunc(makeDBRollback(r)).Export("rollback").
		NewFunctionBuilder().WithFunc(makeDBQuery(r, db, false)).Export("query").
		NewFunctionBuilder().WithFunc(makeDBQuery(r, db, true)).Export("query_replica").
		NewFunctionBuilder().WithFunc(makeDBExec(r, db)).Export("exec").
		NewFunctionBuilder().WithFunc(makeDBExecBatch(r, db)).Export("exec_batch").
		NewFunctionBuilder().WithFunc(makeDBLock(r)).Export("lock").
		NewFunctionBuilder().WithFunc(makeDBNotify(r, db)).Export("notify").
		NewFunctionBuilder().WithFunc(makeDBMigrationDDL(r)).Export("migration_ddl").
		Instantiate(ctx)
	return err
}

func makeDBBegin(r *Runtime, db *sql.DB) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapDBWrite) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("db.write"))
		}

		if modCtx.HasOpenTransaction() {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeTransactionAlreadyOpen,
				Message: "a transaction is already open in this request context",
			})
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.DBBeginInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		isolation, err := isolationLevel(input.Isolation)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		if !r.txLimiter.TryAcquire() {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeTransactionLimitExceeded,
				Message: "maximum concurrent transactions reached",
			})
		}

		target, err := modCtx.database(db)
		if err != nil {
			r.txLimiter.Release()
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()})
		}

		// Pin the connection so exec_batch can access its raw pgx handle.
		conn, err := target.Conn(ctx)
		if err != nil {
			r.txLimiter.Release()
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: err.Error(),
				Retry:   true,
			})
		}

		tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: isolation, ReadOnly: input.ReadOnly})
		if err != nil {
			_ = conn.Close()
			r.txLimiter.Release()
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: err.Error(),
				Retry:   true,
			})
		}

		if err := applyTenantScope(ctx, tx, modCtx); err != nil {
			_ = tx.Rollback()
			_ = conn.Close()
			r.txLimiter.Release()
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: err.Error(),
				Retry:   true,
			})
		}

		txID := uuid.New().String()
		modCtx.RegisterTransaction(txID, conn, tx)

		return abi.WriteToModule(ctx, m, allocate, abiv1.DBBeginOutput{
			TxID:      txID,
			ExpiresAt: time.Now().Add(transactionExpiry).Unix(),
		})
	}
}

func makeDBCommit(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
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
		var input abiv1.DBTxIDInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeTransactionNotFound,
				Message: "transaction ID does not exist or has expired",
			})
		}

		if modCtx.isManagedTransaction(input.TxID) {
			return abi.EncodeHostError(ctx, m, allocate, managedTransactionError())
		}

		afterCommit := modCtx.afterCommitHooks(input.TxID)
		start := time.Now()
		err = tx.Commit()
		modCtx.RemoveTransaction(input.TxID)
		r.txLimiter.Release()
		if err != nil {
			hostErr := &abiv1.HostError{Code: abiv1.ErrCodeCommitFailed, Message: err.Error()}
			// Only serialization failures allow retrying the whole transaction.
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "40001" {
				hostErr.Details = map[string]any{"detail": "serialization_failure"}
				hostErr.Retry = true
			}
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		duration := time.Since(start)

		for _, fn := range afterCommit {
			fn(ctx)
		}
		return abi.WriteToModule(ctx, m, allocate, abiv1.DBDurationOutput{DurationMs: float64(duration.Microseconds()) / 1000})
	}
}

func makeDBRollback(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
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
		var input abiv1.DBTxIDInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.WriteToModule(ctx, m, allocate, abiv1.DBDurationOutput{})
		}

		if modCtx.isManagedTransaction(input.TxID) {
			return abi.EncodeHostError(ctx, m, allocate, managedTransactionError())
		}

		start := time.Now()
		_ = tx.Rollback()
		modCtx.RemoveTransaction(input.TxID)
		r.txLimiter.Release()

		return abi.WriteToModule(ctx, m, allocate, abiv1.DBDurationOutput{DurationMs: float64(time.Since(start).Microseconds()) / 1000})
	}
}

func isolationLevel(name string) (sql.IsolationLevel, error) {
	switch name {
	case "", "read_committed":
		return sql.LevelReadCommitted, nil
	case "repeatable_read":
		return sql.LevelRepeatableRead, nil
	case "serializable":
		return sql.LevelSerializable, nil
	default:
		return 0, errors.New("unknown isolation level " + name)
	}
}
