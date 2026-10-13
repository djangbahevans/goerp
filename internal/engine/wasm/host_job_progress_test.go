package wasm

import (
	"context"
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobprogress"
)

func TestJobProgressHostCallsAndNoOp(t *testing.T) {
	r := newTestRuntime(t, 4<<20)
	mc := newJobsTestModuleContext("tenant", 0)
	binary := buildHostCallerModule("host.jobs", []string{"report_progress", "report_result"})
	inst := newHostCallerInstance(t, t.Context(), r, binary, mc)
	if _, ok := inst.module.Memory().Grow(16); !ok {
		t.Fatal("grow result test memory")
	}
	var got jobprogress.Report
	for _, active := range []bool{false, true} {
		if active {
			mc.JobProgress = jobprogress.NewAttempt(func(_ context.Context, report jobprogress.Report) error { got = report; return nil })
		}
		out := callHost(t, t.Context(), inst, "call_report_progress", abiv1.JobsReportProgressInput{Percent: 200, Message: strings.Repeat("é", 600)})
		if !out.OK {
			t.Fatal(out.Error)
		}
		for _, test := range []struct {
			value string
			code  string
		}{
			{`{"ok":true}`, ""}, {"invalid", abiv1.ErrCodeJobsInvalidResult}, {strings.Repeat("x", (256<<10)+1), abiv1.ErrCodeJobsResultTooLarge},
		} {
			out = callHost(t, t.Context(), inst, "call_report_result", abiv1.JobsReportResultInput{Value: []byte(test.value)})
			if (out.OK) != (test.code == "") || (!out.OK && out.Error.Code != test.code) {
				t.Fatalf("result error=%v want=%s", out.Error, test.code)
			}
		}
		if active {
			mc.JobProgress.Finish(true)
		}
	}
	if got.Percent != 100 || len([]rune(got.Message)) != 500 || string(got.Result) != `{"ok":true}` {
		t.Fatalf("report=%+v", got)
	}
}

func TestJobProgressHostCallsWithoutInvocation(t *testing.T) {
	r := newTestRuntime(t, 4<<20)
	binary := buildHostCallerModule("host.jobs", []string{"report_progress", "report_result"})
	inst := newHostCallerInstance(t, t.Context(), r, binary, nil)
	for _, call := range []struct {
		name  string
		input any
	}{
		{"call_report_progress", abiv1.JobsReportProgressInput{Percent: 20}},
		{"call_report_result", abiv1.JobsReportResultInput{Value: []byte(`null`)}},
	} {
		if out := callHost(t, t.Context(), inst, call.name, call.input); !out.OK {
			t.Fatal(out.Error)
		}
	}
}
