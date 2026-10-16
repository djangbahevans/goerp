package modulereload

import (
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestLeaderReloadSharesPoolBorrowHistogram(t *testing.T) {
	metrics := prometheus.NewRegistry()
	env := newTestEnv(t, wasm.WithMetrics(metrics))
	l, reg := newLeader(t, env, nil)

	name := "metrics_" + uniqueSlug(t)
	binary := compileFixture(t, "")
	src, mf := buildSource(t, name, "1.0.0", binary, nil)
	if err := l.Run(t.Context(), name, src, mf); err != nil {
		t.Fatal(err)
	}
	old := reg.Snapshot().Modules()[name].Pool
	inst, err := old.Borrow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	old.Return(inst)

	src, mf = buildSource(t, name, "1.1.0", binary, nil)
	if err := l.Run(t.Context(), name, src, mf); err != nil {
		t.Fatal(err)
	}
	if err := old.DrainAndClose(t.Context(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	replacement := reg.Snapshot().Modules()[name].Pool
	if replacement == old {
		t.Fatal("reload retained the retired pool")
	}
	inst, err = replacement.Borrow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	replacement.Return(inst)

	families, err := metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(families, func(f *dto.MetricFamily) bool {
		return f.GetName() == "erp_wasm_pool_borrow_duration_seconds"
	})
	if index < 0 || len(families[index].Metric) != 1 {
		t.Fatalf("reload must expose exactly one borrow-duration module series: %v", families)
	}
	metric := families[index].Metric[0]
	if len(metric.Label) != 1 || metric.Label[0].GetName() != "module" || metric.Label[0].GetValue() != name {
		t.Errorf("borrow labels = %v, want module=%s", metric.Label, name)
	}
	if got := metric.GetHistogram().GetSampleCount(); got != 2 {
		t.Errorf("borrow observations = %d, want both pool generations", got)
	}
}
