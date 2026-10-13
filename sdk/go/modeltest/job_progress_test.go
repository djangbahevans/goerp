package modeltest

import (
	"testing"

	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/asyncfixture"
)

func TestRunJobProgressAndResult(t *testing.T) {
	h := asyncHarness(t)
	if err := h.RunJob(fixture.Process, fixture.Payload{Mode: "progress"}); err != nil {
		t.Fatal(err)
	}
	report := h.Jobs.LastRun(fixture.Process)
	result, err := report.DecodeResult[map[string]int]()
	if err != nil || result["created"] != 2 || report.Percent != 43 || report.Message != "imported" {
		t.Fatalf("report=%+v result=%v err=%v", report, result, err)
	}

	for _, mode := range []string{"progress_fail", "progress_large"} {
		if err := h.RunJob(fixture.Process, fixture.Payload{Mode: mode}); err == nil {
			t.Fatalf("%s succeeded", mode)
		}
		report = h.Jobs.LastRun(fixture.Process)
		if report.Percent != 43 || report.Message != "imported" || report.Result != nil {
			t.Fatalf("%s report=%+v", mode, report)
		}
	}
}
