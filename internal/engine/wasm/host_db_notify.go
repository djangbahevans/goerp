package wasm

import (
	"context"
	"database/sql"
	"errors"
	"time"

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

type dbNotifyInput struct {
	Channel string `msgpack:"channel"`
	Payload string `msgpack:"payload"`
	TxID    string `msgpack:"tx_id"`
}

// makeDBNotify builds host.db.notify — a tenant-namespaced Postgres
// NOTIFY, sent immediately against primary if no tx_id is supplied, or on
// the caller's own open host.db.begin transaction otherwise. Postgres
// itself defers a transactional NOTIFY's delivery until COMMIT (and drops
// it on rollback), so this needs no commit-hook mechanism of its own.
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
		var input dbNotifyInput
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
				return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeTransactionNotFound, Message: "transaction ID does not exist or has expired"})
			}
			notifyErr = notifyOnTx(qCtx, tx, channel, input.Payload)
		} else {
			_, notifyErr = primary.ExecContext(qCtx, "SELECT pg_notify($1, $2)", channel, input.Payload)
		}
		if notifyErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, translateNotifyError(notifyErr))
		}

		return abi.WriteToModule(ctx, m, allocate, dbDurationOutput{DurationMs: float64(time.Since(start).Microseconds()) / 1000})
	}
}

// notifyOnTx runs pg_notify on tx inside a SAVEPOINT — a permanent failure
// like an oversized payload (translateNotifyError) must not poison the
// caller's own transaction the way an unprotected ExecContext error would,
// the same concern host.db.lock's own SAVEPOINT protects against. Mirrors
// makeDBLock's own rollback-on-any-post-savepoint-failure shape: rolling
// back to the savepoint after a successful pg_notify but a failed RELEASE
// is still correct, since Postgres rolls back a savepoint's own queued
// NOTIFYs along with everything else it did — a caller told this call
// failed must not have a notification silently still queued underneath.
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

// translateNotifyError maps a pg_notify failure to its HostError — an
// oversized payload (Postgres's own 8000-byte NOTIFY cap) is permanent and
// must never carry Retry: true, unlike a genuine connectivity failure.
// Every other failure (including a transaction already left aborted by
// some earlier, unrelated call) is reported without Retry — matching
// host.db.lock's own generic failure path — since blindly retrying an
// aborted transaction can never succeed without an explicit rollback
// first, and this function has no way to distinguish that case from a
// real transient one.
func translateNotifyError(err error) *abi.HostError {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == invalidParameterValueSQLState {
		return &abi.HostError{Code: abi.ErrCodeExecError, Message: pgErr.Message}
	}
	return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
}
