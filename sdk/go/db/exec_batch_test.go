package db

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

// TestExecBatchResultFromDetails_RealMsgpackRoundTrip builds Details the
// way it actually arrives on the wire — msgpack.Marshal of plain Go
// int/struct values, then Unmarshal into map[string]any, exactly what
// hostcall.Do does for an incoming error response — rather than
// hand-built int64(...) literals. msgpack picks its own wire-compact
// type for a small non-negative int (confirmed empirically: it comes
// back as int8, not int64 or plain int), which a hand-built test can't
// catch; this one does.
func TestExecBatchResultFromDetails_RealMsgpackRoundTrip(t *testing.T) {
	type wireBatchRowError struct {
		Index   int            `msgpack:"index"`
		Code    string         `msgpack:"code"`
		Message string         `msgpack:"message"`
		Details map[string]any `msgpack:"details,omitempty"`
	}
	type wireDetails struct {
		Details map[string]any `msgpack:"details"`
	}

	original := wireDetails{Details: map[string]any{
		"total_rows_affected": 3,
		"failed_count":        1,
		"errors": []wireBatchRowError{
			{Index: 1, Code: "db.unique_violation", Message: "duplicate key", Details: map[string]any{"constraint": "widget_name_key"}},
		},
	}}
	data, err := msgpack.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded wireDetails
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := execBatchResultFromDetails(decoded.Details)
	if got.TotalRowsAffected != 3 {
		t.Errorf("TotalRowsAffected = %d, want 3", got.TotalRowsAffected)
	}
	if got.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", got.FailedCount)
	}
	if len(got.Errors) != 1 {
		t.Fatalf("Errors = %v, want 1 entry", got.Errors)
	}
	rowErr := got.Errors[0]
	if rowErr.Index != 1 || rowErr.Code != "db.unique_violation" || rowErr.Message != "duplicate key" {
		t.Errorf("Errors[0] = %+v", rowErr)
	}
	if rowErr.Details["constraint"] != "widget_name_key" {
		t.Errorf("Errors[0].Details = %v", rowErr.Details)
	}
}

func TestExecBatchResultFromDetails_MissingKeysDefaultToZero(t *testing.T) {
	got := execBatchResultFromDetails(map[string]any{})
	if got.TotalRowsAffected != 0 || got.FailedCount != 0 || got.Errors != nil {
		t.Errorf("got = %+v, want zero value", got)
	}
}

func TestInt64FromAny(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{int64(5), 5},
		{int(5), 5},
		{5.0, 5},
		{nil, 0},
		{"5", 0},
		// The types msgpack actually produces for a small non-negative
		// int decoded into interface{} — see this test file's own
		// RealMsgpackRoundTrip test, which confirmed int8 empirically.
		{int8(5), 5},
		{uint8(5), 5},
		{int16(5), 5},
		{uint16(5), 5},
		{int32(5), 5},
		{uint32(5), 5},
		{uint64(5), 5},
	}
	for _, c := range cases {
		if got := int64FromAny(c.in); got != c.want {
			t.Errorf("int64FromAny(%v %T) = %d, want %d", c.in, c.in, got, c.want)
		}
	}
}

func TestBatchRowErrorsFromAny_NonSliceReturnsNil(t *testing.T) {
	if got := batchRowErrorsFromAny("not a slice"); got != nil {
		t.Errorf("batchRowErrorsFromAny(non-slice) = %v, want nil", got)
	}
}
