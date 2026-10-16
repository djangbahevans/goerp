package tenantsync

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/prometheus/client_golang/prometheus"
)

func TestSchemaSyncMetrics(t *testing.T) {
	env := newTestEnv(t)
	slug := uniqueSlug(t)
	tn := env.activeTenant(t, slug)
	mod := loadedModule(t, "metrics_"+slug, widgetModel())
	reg := prometheus.NewRegistry()
	m := metric.New(reg)
	env.pool.Metrics = m
	env.diffEngine = schema.NewSchemaDiffEngine(&schema.Config{Metrics: m})
	for range 2 {
		if err := SyncOne(t.Context(), env.pool, env.diffEngine, tn, mod, nil); err != nil {
			t.Fatal(err)
		}
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var duration, operation bool
	for _, family := range families {
		switch family.GetName() {
		case "erp_schema_sync_duration_seconds":
			duration = len(family.Metric) == 1 && family.Metric[0].GetHistogram().GetSampleCount() == 2
		case "erp_schema_sync_operations_total":
			for _, sample := range family.Metric {
				for _, label := range sample.Label {
					if label.GetName() == "operation_type" && label.GetValue() == "add_table" && sample.GetCounter().GetValue() == 1 {
						operation = true
					}
				}
			}
		}
	}
	if !duration || !operation {
		t.Fatalf("sync metrics duration=%v, applied operation=%v: %v", duration, operation, families)
	}
}
