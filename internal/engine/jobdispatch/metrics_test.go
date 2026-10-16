package jobdispatch

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestJobAttemptMetrics(t *testing.T) {
	for _, tc := range []struct {
		status  int32
		outcome string
	}{{0, "success"}, {1, "error"}, {2, "cancelled"}} {
		t.Run(tc.outcome, func(t *testing.T) {
			w, tenantID := newTestWorker(t, buildHandleJobConstStatusModule(tc.status))
			reg := prometheus.NewRegistry()
			w.Metrics = metric.New(reg)
			_ = runWork(t, w, jobqueue.WASMJobArgs{ModuleName: testModuleName, JobType: testJobType, TenantID: tenantID})
			if err := testutil.GatherAndCompare(reg, strings.NewReader(fmt.Sprintf(`
# HELP erp_job_executed_total Job execution attempts by outcome.
# TYPE erp_job_executed_total counter
erp_job_executed_total{job_type="test_job",module="testmodule",status="%s"} 1
`, tc.outcome)), "erp_job_executed_total"); err != nil {
				t.Fatal(err)
			}
			families, err := reg.Gather()
			if err != nil {
				t.Fatal(err)
			}
			for _, family := range families {
				if family.GetName() == "erp_job_duration_seconds" && family.Metric[0].GetHistogram().GetSampleCount() != 1 {
					t.Fatalf("duration: %v", family)
				}
			}
		})
	}
}

func TestProviderJobMetricLabels(t *testing.T) {
	w, tenantID := newTestWorker(t, buildHandleJobConstStatusModule(0))
	modules := maps.Clone(w.ModuleRegistry.Snapshot().Modules())
	provider := new(*modules[testModuleName])
	provider.Manifest.Provides = map[string]bool{"payment_provider": true}
	modules[testModuleName] = provider
	if _, err := w.ModuleRegistry.Update(modules); err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	w.Metrics = metric.New(reg)
	for _, name := range []string{"payment-123", "payment-456"} {
		if err := runWork(t, w, jobqueue.WASMJobArgs{
			ModuleName: testModuleName, JobType: name, TenantID: tenantID, ProviderCategory: "payment_provider",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP erp_job_executed_total Job execution attempts by outcome.
# TYPE erp_job_executed_total counter
erp_job_executed_total{job_type="provider",module="testmodule",status="success"} 2
`), "erp_job_executed_total"); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownJobMetricLabels(t *testing.T) {
	w, _ := newTestWorker(t, buildHandleJobConstStatusModule(0))
	reg := prometheus.NewRegistry()
	w.Metrics = metric.New(reg)
	for _, name := range []string{"record-123", "record-456"} {
		_ = runWork(t, w, jobqueue.WASMJobArgs{ModuleName: name, JobType: name})
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP erp_job_executed_total Job execution attempts by outcome.
# TYPE erp_job_executed_total counter
erp_job_executed_total{job_type="unmatched",module="",status="error"} 2
`), "erp_job_executed_total"); err != nil {
		t.Fatal(err)
	}
}
