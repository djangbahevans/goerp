package wasm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// transactionExpiry is the "expires_at" host-abi-reference.md's
// host.db.begin documents — a backstop for the case the engine itself
// crashes before invokeHandler's defer (ModuleContext.RollbackAll) ever
// runs, not the normal cleanup path.
const transactionExpiry = 30 * time.Second

// registerHostDB attaches host.db.begin/commit/rollback to the runtime.
// It lives in the wasm package (not abi, where every other namespace is
// registered) because its closures need direct access to *sql.DB, the
// Runtime's instance registry, and the Runtime's TransactionLimiter — all
// wasm-package types abi cannot import without an import cycle (wasm
// already imports abi for CapabilitySet/HostError).
func registerHostDB(ctx context.Context, rt wazero.Runtime, r *Runtime, db *sql.DB) error {
	_, err := rt.NewHostModuleBuilder("host.db").
		NewFunctionBuilder().WithFunc(makeDBBegin(r, db)).Export("begin").
		NewFunctionBuilder().WithFunc(makeDBCommit(r)).Export("commit").
		NewFunctionBuilder().WithFunc(makeDBRollback(r)).Export("rollback").
		NewFunctionBuilder().WithFunc(makeDBQuery(r, db, false)).Export("query").
		NewFunctionBuilder().WithFunc(makeDBQuery(r, db, true)).Export("query_replica").
		NewFunctionBuilder().WithFunc(makeDBExec(r, db)).Export("exec").
		Instantiate(ctx)
	return err
}

type dbBeginInput struct {
	Isolation string `msgpack:"isolation"`
	ReadOnly  bool   `msgpack:"read_only"`
}

type dbBeginOutput struct {
	TxID      string `msgpack:"tx_id"`
	ExpiresAt int64  `msgpack:"expires_at"`
}

type dbTxIDInput struct {
	TxID string `msgpack:"tx_id"`
}

type dbDurationOutput struct {
	DurationMs float64 `msgpack:"duration_ms"`
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
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    abi.ErrCodeTransactionAlreadyOpen,
				Message: "a transaction is already open in this request context",
			})
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input dbBeginInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		isolation, err := isolationLevel(input.Isolation)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		if !r.txLimiter.TryAcquire() {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    abi.ErrCodeTransactionLimitExceeded,
				Message: "maximum concurrent transactions reached",
			})
		}

		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: isolation, ReadOnly: input.ReadOnly})
		if err != nil {
			r.txLimiter.Release()
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    abi.ErrCodeUnavailable,
				Message: err.Error(),
				Retry:   true,
			})
		}

		// search_path and the ABAC session vars — see applyTenantScope's own
		// doc comment (multitenancy-internals.md §5's "PgBouncer correctness
		// note"). Discarded automatically at commit/rollback — never leaks
		// into whatever this backend connection is reused for next.
		if err := applyTenantScope(ctx, tx, modCtx); err != nil {
			_ = tx.Rollback()
			r.txLimiter.Release()
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    abi.ErrCodeUnavailable,
				Message: err.Error(),
				Retry:   true,
			})
		}

		txID := uuid.NewString()
		modCtx.RegisterTransaction(txID, tx)

		return abi.WriteToModule(ctx, m, allocate, dbBeginOutput{
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
		var input dbTxIDInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    abi.ErrCodeTransactionNotFound,
				Message: "transaction ID does not exist or has expired",
			})
		}

		start := time.Now()
		err = tx.Commit()
		modCtx.RemoveTransaction(input.TxID)
		r.txLimiter.Release()
		if err != nil {
			hostErr := &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
			// Retry is only meaningful for a serialization failure — the
			// module should retry the whole transaction from begin
			// (host-abi-reference.md §5 "host.db.commit"). Other commit
			// failures aren't necessarily safe to blindly retry.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "40001" {
				hostErr.Details = map[string]any{"detail": "serialization_failure"}
				hostErr.Retry = true
			}
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		return abi.WriteToModule(ctx, m, allocate, dbDurationOutput{DurationMs: float64(time.Since(start).Microseconds()) / 1000})
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
		var input dbTxIDInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			// Rollback is safe to call even after a successful commit — a
			// no-op success, not an error (host-abi-reference.md §5
			// "host.db.rollback").
			return abi.WriteToModule(ctx, m, allocate, dbDurationOutput{})
		}

		start := time.Now()
		_ = tx.Rollback()
		modCtx.RemoveTransaction(input.TxID)
		r.txLimiter.Release()

		return abi.WriteToModule(ctx, m, allocate, dbDurationOutput{DurationMs: float64(time.Since(start).Microseconds()) / 1000})
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
