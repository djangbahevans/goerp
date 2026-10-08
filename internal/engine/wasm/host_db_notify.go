package wasm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// invalidParameterValueSQLState is Postgres's SQLSTATE for pg_notify's own
// "payload string too long" error (the 8000-byte NOTIFY payload cap) — a
// permanent failure, never worth retrying. Confirmed against a real
// Postgres instance (Async_Notify, async.c), not assumed from the generic
// "Program Limit Exceeded" class the payload cap might otherwise suggest.
const invalidParameterValueSQLState = "22023"

// Postgres delivers transactional NOTIFY only on commit and discards it on rollback.
func makeDBNotify(r *Runtime, primary *sql.DB) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapDBNotify) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("db.notify"))
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.DBNotifyInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		// Tenant-namespaced so two tenants' modules can never collide on
		// the same channel — a plain string prefix, unlike host.db.lock's
		// xxHash-to-bigint scheme, since pg_notify's channel argument is
		// an arbitrary string, not a Postgres identifier that needs
		// quoting or a fixed-width key.
		channel := modCtx.TenantSlug + ":" + input.Channel

		qCtx, cancel := context.WithTimeout(ctx, defaultExecTimeout)
		defer cancel()

		start := time.Now()
		var notifyErr error
		if input.TxID != "" {
			tx, ok := modCtx.Transaction(input.TxID)
			if !ok {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeTransactionNotFound, Message: "transaction ID does not exist or has expired"})
			}
			notifyErr = notifyOnTx(qCtx, tx, channel, input.Payload)
		} else {
			target, err := modCtx.database(primary)
			if err != nil {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()})
			}

			_, notifyErr = target.ExecContext(qCtx, "SELECT pg_notify($1, $2)", channel, input.Payload)
		}
		if notifyErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, translateNotifyError(notifyErr))
		}

		return abi.WriteToModule(ctx, m, allocate, abiv1.DBDurationOutput{DurationMs: float64(time.Since(start).Microseconds()) / 1000})
	}
}

// A savepoint prevents notification errors from aborting the caller's transaction. Rolling
// back after a failed RELEASE also removes queued notifications, so a reported failure
// cannot leave one pending.
func notifyOnTx(ctx context.Context, tx *sql.Tx, channel, payload string) error {
	if _, err := tx.ExecContext(ctx, "SAVEPOINT notify_attempt"); err != nil {
		return err
	}
	rollbackAndFail := func(err error) error {
		_, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT notify_attempt")
		return err
	}

	if _, err := tx.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, payload); err != nil {
		return rollbackAndFail(err)
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT notify_attempt"); err != nil {
		return rollbackAndFail(err)
	}
	return nil
}

// Oversized NOTIFY payloads are permanent errors. Other failures do not claim retryability
// because an aborted transaction requires explicit rollback.
func translateNotifyError(err error) *abiv1.HostError {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == invalidParameterValueSQLState {
		return &abiv1.HostError{Code: abiv1.ErrCodeExecError, Message: pgErr.Message}
	}
	return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
}
