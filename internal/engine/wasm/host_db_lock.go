package wasm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// lockTimeoutSQLState is Postgres's SQLSTATE for a lock_timeout cancellation.
const lockTimeoutSQLState = "55P03"

type dbLockInput struct {
	Key       string `msgpack:"key"`
	TxID      string `msgpack:"tx_id"`
	TimeoutMs int64  `msgpack:"timeout_ms"`
	Shared    bool   `msgpack:"shared"`
}

type dbLockOutput struct {
	Acquired   bool    `msgpack:"acquired"`
	DurationMs float64 `msgpack:"duration_ms"`
}

// makeDBLock builds host.db.lock — a tenant-namespaced Postgres advisory
// lock scoped to the caller's own open host.db.begin transaction. TimeoutMs
// is taken at face value (0 = try-lock); sdk/go/db's Lock/TryLock
// (goerp#508) is what supplies a friendlier default, not this function.
func makeDBLock(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
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
		var input dbLockInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		tx, ok := modCtx.Transaction(input.TxID)
		if !ok {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeTransactionNotFound, Message: "transaction ID does not exist or has expired"})
		}

		// Tenant-namespaced so two tenants never collide, hashed to the
		// bigint Postgres's advisory-lock functions take via xxHash —
		// host-abi-reference.md's own documented choice for this call.
		lockKey := int64(xxhash.Sum64String(modCtx.TenantSlug + ":" + input.Key))

		lockFn, tryLockFn := "pg_advisory_xact_lock", "pg_try_advisory_xact_lock"
		if input.Shared {
			lockFn, tryLockFn = lockFn+"_shared", tryLockFn+"_shared"
		}

		start := time.Now()
		fail := func(err error) uint64 {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()})
		}

		if input.TimeoutMs == 0 {
			var acquired bool
			if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT %s($1)", tryLockFn), lockKey).Scan(&acquired); err != nil {
				return fail(err)
			}
			return abi.WriteToModule(ctx, m, allocate, dbLockOutput{
				Acquired:   acquired,
				DurationMs: float64(time.Since(start).Microseconds()) / 1000,
			})
		}

		// A SAVEPOINT scopes the lock_timeout GUC change and the blocking
		// attempt together — any error past this point (an out-of-range
		// timeout, the lock_timeout cancellation itself, anything) aborts
		// the transaction block until rolled back, so every such path
		// below goes through rollbackAndFail rather than a bare fail.
		if _, err := tx.ExecContext(ctx, "SAVEPOINT lock_attempt"); err != nil {
			return fail(err)
		}
		rollbackAndFail := func(err error) uint64 {
			_, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT lock_attempt")
			return fail(err)
		}

		if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", input.TimeoutMs)); err != nil {
			return rollbackAndFail(err)
		}

		_, lockErr := tx.ExecContext(ctx, fmt.Sprintf("SELECT %s($1)", lockFn), lockKey)
		duration := float64(time.Since(start).Microseconds()) / 1000

		if lockErr == nil {
			// Reset before releasing the savepoint, not after — if the
			// reset itself fails, there's still a savepoint to roll back
			// to (rollbackAndFail also releases the lock this acquired,
			// which is correct: this call is reporting an error, so the
			// caller must not believe it holds the lock).
			if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = DEFAULT"); err != nil {
				return rollbackAndFail(err)
			}
			if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT lock_attempt"); err != nil {
				return rollbackAndFail(err)
			}
			return abi.WriteToModule(ctx, m, allocate, dbLockOutput{Acquired: true, DurationMs: duration})
		}

		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT lock_attempt"); err != nil {
			return fail(err)
		}
		if pgErr, ok := errors.AsType[*pgconn.PgError](lockErr); ok && pgErr.Code == lockTimeoutSQLState {
			return abi.WriteToModule(ctx, m, allocate, dbLockOutput{Acquired: false, DurationMs: duration})
		}
		return fail(lockErr)
	}
}
