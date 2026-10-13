package jobprogress

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestAttemptCoalescesAndFlushesFinalResult(t *testing.T) {
	var mu sync.Mutex
	var writes []Report
	var times []time.Time
	saved := make(chan struct{}, 10)
	a := NewAttempt(func(_ context.Context, report Report) error {
		mu.Lock()
		writes = append(writes, report)
		times = append(times, time.Now())
		mu.Unlock()
		saved <- struct{}{}
		return nil
	})
	if err := a.ReportResult([]byte(`{"first":true}`)); err != nil {
		t.Fatal(err)
	}
	for i := range 10000 {
		a.ReportProgress(i, strings.Repeat("é", 600))
	}
	for range 2 {
		select {
		case <-saved:
			a.ReportProgress(-1, "latest")
		case <-time.After(3 * time.Second):
			t.Fatal("progress not flushed within a second")
		}
	}
	if err := a.ReportResult([]byte(`{"last":true}`)); err != nil {
		t.Fatal(err)
	}
	a.ReportProgress(73, "final")
	a.Finish(true)

	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 3 {
		t.Fatalf("got %d writes, want 2 periodic and 1 final", len(writes))
	}
	if times[1].Sub(times[0]) < 900*time.Millisecond {
		t.Fatal("periodic writes exceeded once per second")
	}
	if writes[0].Percent != 100 || len([]rune(writes[0].Message)) != 500 || writes[1].Percent != 0 {
		t.Fatalf("clamping: %+v", writes)
	}
	for _, report := range writes[:2] {
		if report.Result != nil {
			t.Fatal("result persisted before success")
		}
	}
	final := writes[2]
	if final.Percent != 73 || final.Message != "final" || string(final.Result) != `{"last":true}` {
		t.Fatalf("final report = %+v", final)
	}
}

func TestFailedAttemptDropsResultAndStorageFailureDoesNotFailHandler(t *testing.T) {
	var final Report
	a := NewAttempt(func(_ context.Context, report Report) error { final = report; return errors.New("storage unavailable") })
	a.ReportProgress(28, "partial")
	if err := a.ReportResult([]byte(`null`)); err != nil {
		t.Fatal(err)
	}
	a.Finish(false)
	if final.Percent != 28 || final.Result != nil {
		t.Fatalf("failed report = %+v", final)
	}

	calls := 0
	unused := NewAttempt(func(context.Context, Report) error { calls++; return nil })
	unused.Finish(true)
	if calls != 0 {
		t.Fatal("empty attempt created a row")
	}
}

func TestValidateResult(t *testing.T) {
	for _, test := range []struct {
		value []byte
		code  string
	}{
		{[]byte(`null`), ""},
		{[]byte(`{"ok":true}`), ""},
		{[]byte(`not JSON`), abi.ErrCodeJobsInvalidResult},
		{[]byte(`{} {}`), abi.ErrCodeJobsInvalidResult},
		{[]byte(`"` + strings.Repeat("a", MaxResultBytes-2) + `"`), ""},
		{[]byte(`"` + strings.Repeat("a", MaxResultBytes-1) + `"`), abi.ErrCodeJobsResultTooLarge},
	} {
		err := ValidateResult(test.value)
		if (err == nil) != (test.code == "") || (err != nil && err.Code != test.code) {
			t.Fatalf("validation = %v, want %s", err, test.code)
		}
	}
	var report Report
	a := NewAttempt(func(_ context.Context, r Report) error { report = r; return nil })
	value := jsontext.Value(`{"ok":true}`)
	if err := a.ReportResult(value); err != nil {
		t.Fatal(err)
	}
	value[2] = 'x'
	if err := a.ReportResult([]byte("bad")); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	a.Finish(true)
	if string(report.Result) != `{"ok":true}` {
		t.Fatalf("invalid call or caller mutation changed result: %s", report.Result)
	}
}

func TestSnapshotPreservesExplicitEmptyResults(t *testing.T) {
	for _, value := range []string{"", "null", "{}", "[]", `""`, "0", "false"} {
		snapshot := Snapshot{Status: "complete"}
		if value != "" {
			snapshot.Result = []byte(value)
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		result, present := fields["result"]
		if present != (value != "") || string(result) != value {
			t.Fatalf("result=%q present=%v for %q", result, present, value)
		}
	}
}
