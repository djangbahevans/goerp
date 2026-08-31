package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// host.db.exec_batch (host-abi-reference.md §5 "host.db.exec_batch"):
// executes one parameterized statement against multiple parameter sets,
// inside a single transaction, by running host_db_exec.go's own
// prepareExec/execRow once per parameter set — the RETURNING
// construction, constraint-violation translation, and etag/audit
// mechanisms this reuses are exactly host.db.exec's own, per goerp#461's
// own scope.
//
// Real Postgres COPY (bulk INSERT) and pipeline mode (UPDATE/DELETE) are
// out of scope here — see goerp#513 and its blocker goerp#511. This
// executes each parameter set sequentially within the batch's own shared
// transaction.

type dbExecBatchOpts struct {
	ContinueOnError bool   `msgpack:"continue_on_error"`
	TimeoutMs       int64  `msgpack:"timeout_ms"`
	Returning       string `msgpack:"returning"`
	SkipAudit       bool   `msgpack:"skip_audit"`
	SkipEtag        bool   `msgpack:"skip_etag"`
}

type dbExecBatchInput struct {
	SQL       string          `msgpack:"sql"`
	ParamSets [][]any         `msgpack:"param_sets"`
	TxID      string          `msgpack:"tx_id"`
	Opts      dbExecBatchOpts `msgpack:"opts"`
}

// batchRowError is one parameter set's own failure — host.db.exec's own
// error code/message/details for that row, plus its position in
// param_sets.
type batchRowError struct {
	Index   int            `msgpack:"index"`
	Code    string         `msgpack:"code"`
	Message string         `msgpack:"message"`
	Details map[string]any `msgpack:"details,omitempty"`
}

// dbExecBatchOutput is only ever returned on a fully-successful batch —
// any parameter-set failure returns via a db.batch_error/
// db.batch_partial_error *abi.HostError instead (host.db.exec_batch's own
// error-model contract: an error replaces the normal response, it never
// accompanies one), so this shape has no failed_count/errors fields of
// its own — a genuinely successful batch has zero failures by
// construction. db.batch_partial_error's own Details carries the
// equivalent total_rows_affected/failed_count/errors/returning summary
// for the partial-failure case; see host-abi-reference.md's own
// "host.db.exec_batch" section.
type dbExecBatchOutput struct {
	TotalRowsAffected int     `msgpack:"total_rows_affected"`
	Returning         [][]any `msgpack:"returning,omitempty"`
	DurationMs        float64 `msgpack:"duration_ms"`
}

func makeDBExecBatch(r *Runtime, primary *sql.DB) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
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
		var input dbExecBatchInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		output, hostErr := DBExecBatch(ctx, primary, modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, output)
	}
}

// runSavepointOp executes sql — a SAVEPOINT/ROLLBACK TO SAVEPOINT/RELEASE
// SAVEPOINT statement — against tx, rolling back the whole batch (via
// finish) and returning an abi.unavailable error on failure. A broken
// savepoint operation indicates connection-level trouble, not a single
// parameter set's own data problem, so it aborts the batch rather than
// being recorded as that row's own failure.
func runSavepointOp(ctx context.Context, tx *sql.Tx, finish func(error) error, sql string) *abi.HostError {
	if _, err := tx.ExecContext(ctx, sql); err != nil {
		_ = finish(err)
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	return nil
}

// DBExecBatch implements host.db.exec_batch (host-abi-reference.md §5).
// Every parameter set runs through execRow (host_db_exec.go) inside one
// transaction shared across the whole batch — the caller's own tx_id if
// supplied, otherwise one this call opens and commits/rolls back around
// the batch as a whole.
//
// opts.continue_on_error wraps each row's execRow call in its own
// SAVEPOINT: a failed statement leaves a Postgres transaction aborted
// until rolled back to that point, so without a savepoint per row, one
// failure would silently doom every subsequent parameter set in the same
// batch rather than being recorded as that row's own failure.
func DBExecBatch(ctx context.Context, primary *sql.DB, modCtx *ModuleContext, input dbExecBatchInput) (dbExecBatchOutput, *abi.HostError) {
	p, hostErr := prepareExec(input.SQL, dbExecOpts{
		Returning: input.Opts.Returning,
		SkipAudit: input.Opts.SkipAudit,
		SkipEtag:  input.Opts.SkipEtag,
	}, modCtx)
	if hostErr != nil {
		return dbExecBatchOutput{}, hostErr
	}

	timeout := defaultExecTimeout
	if input.Opts.TimeoutMs > 0 {
		timeout = time.Duration(input.Opts.TimeoutMs) * time.Millisecond
	}

	// ctx, not a timeout-bound derivative, is what opens the transaction:
	// database/sql ties a transaction's whole lifetime to the context
	// BeginTx was called with — it auto-rolls-back once that context is
	// canceled or its deadline passes, not just during the BeginTx call
	// itself. timeout is a per-row budget (each execRow call below gets
	// its own fresh window); binding it to BeginTx's context instead
	// would silently roll back the whole batch partway through any batch
	// that runs longer than one row's own timeout.
	tx, finish, hostErr := beginOrBorrowExecTx(ctx, primary, modCtx, input.TxID)
	if hostErr != nil {
		return dbExecBatchOutput{}, hostErr
	}

	start := time.Now()

	var (
		totalRowsAffected int
		returning         [][]any
		batchErrors       []batchRowError
	)

	for i, params := range input.ParamSets {
		// One per-row context brackets the SAVEPOINT/exec/ROLLBACK-or-
		// RELEASE sequence as a whole — not just the statement itself —
		// so opts.timeout_ms's own per-parameter-set budget (per
		// host-abi-reference.md) covers every round trip a single
		// parameter set makes, not only its main statement.
		rowCtx, rowCancel := context.WithTimeout(ctx, timeout)

		if input.Opts.ContinueOnError {
			if hostErr := runSavepointOp(rowCtx, tx, finish, "SAVEPOINT exec_batch_row"); hostErr != nil {
				rowCancel()
				return dbExecBatchOutput{}, hostErr
			}
		}

		result, rowErr := execRow(rowCtx, tx, modCtx, p, params)

		if rowErr != nil {
			if !input.Opts.ContinueOnError {
				rowCancel()
				_ = finish(errors.New(rowErr.Message))
				return dbExecBatchOutput{}, &abi.HostError{
					Code:    abi.ErrCodeDBBatchError,
					Message: fmt.Sprintf("parameter set %d: %s", i, rowErr.Message),
					Details: map[string]any{"index": i, "code": rowErr.Code, "message": rowErr.Message, "details": rowErr.Details},
				}
			}

			if hostErr := runSavepointOp(rowCtx, tx, finish, "ROLLBACK TO SAVEPOINT exec_batch_row"); hostErr != nil {
				rowCancel()
				return dbExecBatchOutput{}, hostErr
			}
			if hostErr := runSavepointOp(rowCtx, tx, finish, "RELEASE SAVEPOINT exec_batch_row"); hostErr != nil {
				rowCancel()
				return dbExecBatchOutput{}, hostErr
			}
			rowCancel()
			batchErrors = append(batchErrors, batchRowError{Index: i, Code: rowErr.Code, Message: rowErr.Message, Details: rowErr.Details})
			continue
		}

		if input.Opts.ContinueOnError {
			if hostErr := runSavepointOp(rowCtx, tx, finish, "RELEASE SAVEPOINT exec_batch_row"); hostErr != nil {
				rowCancel()
				return dbExecBatchOutput{}, hostErr
			}
		}
		rowCancel()

		totalRowsAffected += int(result.RowsAffected)
		if p.requestedCols != nil {
			returning = append(returning, result.Returning...)
		}
	}

	if err := finish(nil); err != nil {
		return dbExecBatchOutput{}, &abi.HostError{Code: abi.ErrCodeCommitFailed, Message: err.Error()}
	}
	duration := time.Since(start)

	if duration > slowQueryThreshold {
		log.Warn().Str("module", modCtx.ModuleName).Str("sql", input.SQL).
			Int("param_sets", len(input.ParamSets)).Dur("duration", duration).
			Msg("host.db.exec_batch: slow batch")
	}

	if len(batchErrors) > 0 {
		details := map[string]any{
			"total_rows_affected": totalRowsAffected,
			"failed_count":        len(batchErrors),
			"errors":              batchErrors,
		}
		if p.requestedCols != nil {
			details["returning"] = returning
		}
		return dbExecBatchOutput{}, &abi.HostError{
			Code:    abi.ErrCodeDBBatchPartialError,
			Message: fmt.Sprintf("%d of %d parameter sets failed", len(batchErrors), len(input.ParamSets)),
			Details: details,
		}
	}

	output := dbExecBatchOutput{
		TotalRowsAffected: totalRowsAffected,
		DurationMs:        float64(duration.Microseconds()) / 1000,
	}
	if p.requestedCols != nil {
		output.Returning = returning
	}
	return output, nil
}
