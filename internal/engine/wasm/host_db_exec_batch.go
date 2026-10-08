package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/rs/zerolog/log"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// Batch execution uses COPY for eligible inserts and pipelining for eligible
// updates/deletes. The sequential fallback shares single-statement RETURNING, constraint,
// etag and audit handling.

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
		var input abiv1.DBExecBatchInput
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

// batchErrorForHostErr builds host.db.exec_batch's own db.batch_error
// envelope (host-abi-reference.md §5) attributing a batch failure to
// param_sets[index], from a HostError a lower call already produced —
// shared by every dispatch path (sequential and pipeline) that wraps one
// row's own structured failure this way. index is -1 for a failure no
// single param_sets entry can be blamed for (wrapBatchFailure, and
// captureRowsBeforeExecBatch's own batched pre-read, use the same
// convention).
func batchErrorForHostErr(index int, err *abiv1.HostError) *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeDBBatchError,
		Message: fmt.Sprintf("parameter set %d: %s", index, err.Message),
		Details: map[string]any{"index": index, "code": err.Code, "message": err.Message, "details": err.Details},
	}
}

// batchErrorForRowErr is batchErrorForHostErr's own counterpart for a
// plain Go error with no HostError to unwrap. outerPrefix, when
// non-empty, prefixes the envelope's own top-level Message only (e.g.
// "audit write failed: ") — Details["message"] always stays err's own
// bare text, matching what a caller reading Details["message"] for
// retriable-error triage already expects from batchErrorForHostErr.
func batchErrorForRowErr(index int, code, outerPrefix string, err error) *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeDBBatchError,
		Message: fmt.Sprintf("parameter set %d: %s%s", index, outerPrefix, err.Error()),
		Details: map[string]any{"index": index, "code": code, "message": err.Error()},
	}
}

// finishBatchTx commits/rolls back a batch's transaction via finish and
// measures its own duration, logging a slow-batch warning past
// slowQueryThreshold — the finish+duration+slow-log sequence shared by
// every host.db.exec_batch dispatch path (COPY, pipeline, and the
// sequential path), run before each one's own success/partial-failure
// decision.
func finishBatchTx(finish func(error) error, start time.Time, modCtx *ModuleContext, sqlText string, numParamSets int, logMsg string) (time.Duration, *abiv1.HostError) {
	if err := finish(nil); err != nil {
		return 0, &abiv1.HostError{Code: abiv1.ErrCodeCommitFailed, Message: err.Error()}
	}
	duration := time.Since(start)
	if duration > slowQueryThreshold {
		log.Warn().Str("module", modCtx.ModuleName).Str("sql", sqlText).
			Int("param_sets", numParamSets).Dur("duration", duration).
			Msg(logMsg)
	}
	return duration, nil
}

// batchOutput assembles host.db.exec_batch's own successful output shape
// — shared by every dispatch path's own success case.
func batchOutput(totalRowsAffected int, duration time.Duration, returning [][]any, requestedCols []string) abiv1.DBExecBatchOutput {
	output := abiv1.DBExecBatchOutput{
		TotalRowsAffected: totalRowsAffected,
		DurationMs:        float64(duration.Microseconds()) / 1000,
	}
	if requestedCols != nil {
		output.Returning = returning
	}
	return output
}

// runSavepointOp executes sql — a SAVEPOINT/ROLLBACK TO SAVEPOINT/RELEASE
// SAVEPOINT statement — against tx, rolling back the whole batch (via
// finish) and returning an abi.unavailable error on failure. A broken
// savepoint operation indicates connection-level trouble, not a single
// parameter set's own data problem, so it aborts the batch rather than
// being recorded as that row's own failure.
func runSavepointOp(ctx context.Context, tx *sql.Tx, finish func(error) error, sql string) *abiv1.HostError {
	if _, err := tx.ExecContext(ctx, sql); err != nil {
		_ = finish(err)
		return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
	}
	return nil
}

// DBExecBatch runs all parameter sets in one transaction. continue_on_error uses per-row
// savepoints so a failed statement does not abort subsequent rows. A failed fast-path
// attempt can retry sequentially only after its ephemeral transaction has rolled back
// completely.
func DBExecBatch(ctx context.Context, primary *sql.DB, modCtx *ModuleContext, input abiv1.DBExecBatchInput) (abiv1.DBExecBatchOutput, *abiv1.HostError) {
	p, hostErr := prepareExec(input.SQL, abiv1.DBExecOpts{
		Returning: input.Opts.Returning,
		SkipAudit: input.Opts.SkipAudit,
		SkipEtag:  input.Opts.SkipEtag,
	}, modCtx)
	if hostErr != nil {
		return abiv1.DBExecBatchOutput{}, hostErr
	}

	timeout := defaultExecTimeout
	if input.Opts.TimeoutMs > 0 {
		timeout = time.Duration(input.Opts.TimeoutMs) * time.Millisecond
	}

	numRows := len(input.ParamSets)

	// Sequential retry requires a fully rolled-back fast attempt. A borrowed transaction
	// cannot provide that rollback without disturbing the caller's lifecycle.
	fastPathUsable := !input.Opts.ContinueOnError || input.TxID == ""

	if fastPathUsable {
		if plan := resolveCopyPlan(p, numRows, modCtx); plan.Eligible {
			fastCtx, cancel := context.WithTimeout(ctx, timeout)
			out, fastErr := execBatchCopy(fastCtx, primary, modCtx, p, input, plan)
			cancel()
			if fastErr == nil || !input.Opts.ContinueOnError {
				return out, fastErr
			}
		} else if pipelineEligible(p, input.ParamSets) {
			fastCtx, cancel := context.WithTimeout(ctx, timeout)
			out, fastErr := execBatchPipeline(fastCtx, primary, modCtx, p, input)
			cancel()
			if fastErr == nil || !input.Opts.ContinueOnError {
				return out, fastErr
			}
		}
	}

	// BeginTx retains its context for the transaction lifetime. Bind row timeouts to each
	// execution so one row's deadline cannot roll back the whole batch.
	_, tx, finish, hostErr := beginOrBorrowExecTx(ctx, primary, modCtx, input.TxID)
	if hostErr != nil {
		return abiv1.DBExecBatchOutput{}, hostErr
	}

	start := time.Now()

	var (
		totalRowsAffected int
		returning         [][]any
		batchErrors       []abiv1.DBBatchRowError
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
				return abiv1.DBExecBatchOutput{}, hostErr
			}
		}

		result, rowErr := execRow(rowCtx, tx, modCtx, p, params)

		if rowErr != nil {
			if !input.Opts.ContinueOnError {
				rowCancel()
				_ = finish(errors.New(rowErr.Message))
				return abiv1.DBExecBatchOutput{}, batchErrorForHostErr(i, rowErr)
			}

			if hostErr := runSavepointOp(rowCtx, tx, finish, "ROLLBACK TO SAVEPOINT exec_batch_row"); hostErr != nil {
				rowCancel()
				return abiv1.DBExecBatchOutput{}, hostErr
			}
			if hostErr := runSavepointOp(rowCtx, tx, finish, "RELEASE SAVEPOINT exec_batch_row"); hostErr != nil {
				rowCancel()
				return abiv1.DBExecBatchOutput{}, hostErr
			}
			rowCancel()
			batchErrors = append(batchErrors, abiv1.DBBatchRowError{Index: i, Code: rowErr.Code, Message: rowErr.Message, Details: rowErr.Details})
			continue
		}

		if input.Opts.ContinueOnError {
			if hostErr := runSavepointOp(rowCtx, tx, finish, "RELEASE SAVEPOINT exec_batch_row"); hostErr != nil {
				rowCancel()
				return abiv1.DBExecBatchOutput{}, hostErr
			}
		}
		rowCancel()

		totalRowsAffected += int(result.RowsAffected)
		if p.requestedCols != nil {
			returning = append(returning, result.Returning...)
		}
	}

	duration, hostErr := finishBatchTx(finish, start, modCtx, input.SQL, len(input.ParamSets), "host.db.exec_batch: slow batch")
	if hostErr != nil {
		return abiv1.DBExecBatchOutput{}, hostErr
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
		return abiv1.DBExecBatchOutput{}, &abiv1.HostError{
			Code:    abiv1.ErrCodeDBBatchPartialError,
			Message: fmt.Sprintf("%d of %d parameter sets failed", len(batchErrors), len(input.ParamSets)),
			Details: details,
		}
	}

	return batchOutput(totalRowsAffected, duration, returning, p.requestedCols), nil
}
