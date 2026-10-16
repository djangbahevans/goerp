package jobqueue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/riverqueue/river/rivertype"
)

func TestPlatformJobMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	middleware := metricsMiddleware(metric.New(reg))
	err := middleware(t.Context(), &rivertype.JobRow{Kind: "retention_sweep"}, func(context.Context) error { return errors.New("failed") })
	if err == nil {
		t.Fatal("worker error lost")
	}
	if err := middleware(t.Context(), &rivertype.JobRow{Kind: "wasm_job"}, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP erp_job_executed_total Job execution attempts by outcome.
# TYPE erp_job_executed_total counter
erp_job_executed_total{job_type="retention_sweep",module="",status="error"} 1
`), "erp_job_executed_total"); err != nil {
		t.Fatal(err)
	}
}
