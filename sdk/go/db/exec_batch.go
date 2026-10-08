package db

import (
	"errors"
	"reflect"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// ExecBatchResult is the outcome of an ExecBatch call. FailedCount and
// Errors report per-row failures and are only non-zero when ExecBatch
// returns a nil error.
type ExecBatchResult struct {
	TotalRowsAffected int64
	FailedCount       int
	Errors            []BatchRowError
	DurationMs        float64
}

// BatchRowError is one parameter set's failure within an ExecBatch call.
type BatchRowError struct {
	Index   int
	Code    string
	Message string
	Details map[string]any
}

// ExecBatch executes sql once per entry in argSets inside a single
// transaction. Every parameter set is attempted; per-row failures are
// reported in the result's FailedCount and Errors with a nil error. A
// non-nil error means a batch-level failure (the SQL, the transaction or
// the host call) and that no parameter set ran.
func ExecBatch(sql string, argSets [][]any) (ExecBatchResult, error) {
	in := abi.DBExecBatchInput{SQL: sql, ParamSets: argSets, Opts: abi.DBExecBatchOpts{ContinueOnError: true}}
	var out abi.DBExecBatchOutput
	err := hostcall.Do(hostDBExecBatch, in, &out)
	if err == nil {
		return ExecBatchResult{TotalRowsAffected: int64(out.TotalRowsAffected), DurationMs: out.DurationMs}, nil
	}

	if he, ok := errors.AsType[*abi.HostError](err); ok && he.Code == abi.ErrCodeDBBatchPartialError {
		return execBatchResultFromDetails(he.Details), nil
	}
	return ExecBatchResult{}, wrapExecError(err)
}

// execBatchResultFromDetails unpacks a db.batch_partial_error's Details
// into an ExecBatchResult. The values are msgpack-decoded into any, so
// each field needs its own conversion.
func execBatchResultFromDetails(details map[string]any) ExecBatchResult {
	return ExecBatchResult{
		TotalRowsAffected: int64FromAny(details["total_rows_affected"]),
		FailedCount:       int(int64FromAny(details["failed_count"])),
		Errors:            batchRowErrorsFromAny(details["errors"]),
	}
}

func batchRowErrorsFromAny(raw any) []BatchRowError {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]BatchRowError, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rowErr := BatchRowError{
			Index:   int(int64FromAny(m["index"])),
			Code:    detailString(m, "code"),
			Message: detailString(m, "message"),
		}
		if d, ok := m["details"].(map[string]any); ok {
			rowErr.Details = d
		}
		out = append(out, rowErr)
	}
	return out
}

// int64FromAny converts a msgpack-decoded number into int64. msgpack
// decodes small integers as int8, uint8 and so on, so this switches on
// reflect.Kind. Anything non-numeric, including nil, is 0.
func int64FromAny(v any) int64 {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint())
	case reflect.Float32, reflect.Float64:
		return int64(rv.Float())
	default:
		return 0
	}
}
