package db

import (
	"errors"
	"reflect"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// ExecBatchResult is host.db.exec_batch's own response —
// go-sdk-reference.md §6 "Exec — write rows". FailedCount/Errors are
// only ever non-zero alongside a nil error — see ExecBatch's own doc
// comment.
type ExecBatchResult struct {
	TotalRowsAffected int64
	FailedCount       int
	Errors            []BatchRowError
	DurationMs        float64
}

// BatchRowError is one parameter set's own failure within an ExecBatch
// call — host-abi-reference.md §5's own BatchRowError shape.
type BatchRowError struct {
	Index   int
	Code    string
	Message string
	Details map[string]any
}

type dbExecBatchOpts struct {
	ContinueOnError bool `msgpack:"continue_on_error"`
}

type dbExecBatchInput struct {
	SQL       string          `msgpack:"sql"`
	ParamSets [][]any         `msgpack:"param_sets"`
	Opts      dbExecBatchOpts `msgpack:"opts"`
}

type dbExecBatchOutput struct {
	TotalRowsAffected int     `msgpack:"total_rows_affected"`
	DurationMs        float64 `msgpack:"duration_ms"`
}

// ExecBatch executes sql once per entry in argSets, inside a single
// transaction, via host.db.exec_batch. It always runs with
// continue_on_error: true — every parameter set is attempted, and a
// partial failure is reported through the returned ExecBatchResult's own
// FailedCount/Errors (with err == nil), not as a top-level error. err is
// non-nil only for a batch-level failure — the SQL itself, the
// transaction, or the host call — that means no parameter set ran at
// all.
func ExecBatch(sql string, argSets [][]any) (ExecBatchResult, error) {
	in := dbExecBatchInput{SQL: sql, ParamSets: argSets, Opts: dbExecBatchOpts{ContinueOnError: true}}
	var out dbExecBatchOutput
	err := hostcall.Do(hostDBExecBatch, in, &out)
	if err == nil {
		return ExecBatchResult{TotalRowsAffected: int64(out.TotalRowsAffected), DurationMs: out.DurationMs}, nil
	}

	var he *hostcall.HostError
	if errors.As(err, &he) && he.Code == "db.batch_partial_error" {
		return execBatchResultFromDetails(he.Details), nil
	}
	return ExecBatchResult{}, wrapExecError(err)
}

// execBatchResultFromDetails unpacks db.batch_partial_error's own
// Details (host-abi-reference.md §5's {total_rows_affected,
// failed_count, errors, returning?}) into an ExecBatchResult. The
// values arrived through msgpack decoded into interface{}, so each
// field needs its own type assertion/conversion rather than a direct
// cast.
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

// int64FromAny converts a msgpack-decoded numeric value into int64.
// msgpack picks its own wire-compact type for a Go int depending on
// magnitude — a small non-negative count like total_rows_affected/
// failed_count/index decodes as int8/uint8/int16/... not int64 or even
// plain int (confirmed empirically: msgpack.Marshal/Unmarshal round-
// tripping a small int through a map[string]any field produces int8) —
// so this switches on reflect.Kind rather than hardcoding a couple of
// concrete types, to actually cover whatever the wire sends. Anything
// non-numeric (including nil, for a missing key) is 0.
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
