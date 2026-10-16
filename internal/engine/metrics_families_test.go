package engine

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestEngineMetricFamiliesAndUninstall(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.ModuleDev = true
	cfg.ModuleDir = t.TempDir()
	e, err := New(cfg)
	requireEngineConstruction(t, err)
	closeTestEnginePools(t, e)
	t.Cleanup(func() { _ = e.Shutdown(context.Background()) })
	tn, err := e.tenantStore.CreateTenant(t.Context(), "metrictenant", "Metric tenant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.syncPool.Raw().ExecContext(t.Context(), "UPDATE system.tenants SET status = 'active' WHERE id = $1", tn.ID); err != nil {
		t.Fatal(err)
	}
	ctx := metric.WithModule(t.Context(), "metric_sales")
	compiled, err := e.wasmRuntime.CompileModule(ctx, handleRequestEchoModule)
	if err != nil {
		t.Fatal(err)
	}
	pool := e.wasmRuntime.NewPool("metric_sales", compiled, wasm.PoolConfig{WarmSize: 1, MaxSize: 1, BorrowTimeout: time.Millisecond})
	t.Cleanup(func() { _ = pool.DrainAndClose(context.Background(), time.Second) })
	mod := &module.LoadedModule{Manifest: manifest.Manifest{Name: "metric_sales"}, Status: module.StatusReady, Pool: pool}
	if _, err := e.moduleRegistry.Update(map[string]*module.LoadedModule{"metric_sales": mod}); err != nil {
		t.Fatal(err)
	}
	inst, err := pool.Borrow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Return(inst)
	if _, err := pool.Borrow(t.Context()); !errors.Is(err, wasm.ErrPoolTimeout) {
		t.Fatalf("saturated borrow: %v", err)
	}

	e.metrics.execution.Host("metric_sales", "host.time.now", "success", time.Millisecond)
	e.metrics.execution.Job("metric_sales", "invoice", "success", time.Second)
	e.metrics.execution.Sync("metric_sales", time.Second)
	e.metrics.execution.SchemaOperation("metric_sales", "add_table")
	if _, err := e.primaryDB.ExecContext(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.jobQueue.Insert(t.Context(), jobqueue.WASMJobArgs{ModuleName: "metric_sales", JobType: "invoice"}, nil); err != nil {
		t.Fatal(err)
	}

	expected := map[string][]string{
		"erp_wasm_pool_size":                    {"module", "state"},
		"erp_wasm_pool_borrow_duration_seconds": {"module"},
		"erp_wasm_pool_borrow_timeouts_total":   {"module"},
		"erp_wasm_compilation_duration_seconds": {"module"},
		"erp_host_function_calls_total":         {"function", "module", "status"},
		"erp_host_function_duration_seconds":    {"function", "module"},
		"erp_db_query_duration_seconds":         {"module", "operation"},
		"erp_db_pool_connections":               {"pool", "state"},
		"erp_job_queue_depth":                   {"queue"},
		"erp_job_executed_total":                {"job_type", "module", "status"},
		"erp_job_duration_seconds":              {"job_type", "module"},
		"erp_schema_sync_duration_seconds":      {"module"},
		"erp_schema_sync_operations_total":      {"module", "operation_type"},
		"erp_active_tenants_total":              {},
		"erp_module_status":                     {"module", "status"},
	}
	families, err := e.metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		labels, ok := expected[family.GetName()]
		if !ok {
			continue
		}
		for _, sample := range family.Metric {
			var names []string
			for _, label := range sample.Label {
				names = append(names, label.GetName())
			}
			if !slices.Equal(names, labels) {
				t.Fatalf("%s labels=%v, want %v", family.GetName(), names, labels)
			}
			switch family.GetName() {
			case "erp_wasm_pool_size":
				if sample.Label[1].GetValue() == "in_use" && sample.GetGauge().GetValue() != 1 {
					t.Fatalf("live pool size: %v", sample)
				}
			case "erp_active_tenants_total":
				if sample.GetGauge().GetValue() != 1 {
					t.Fatalf("active tenants: %v", sample)
				}
			case "erp_wasm_pool_borrow_timeouts_total":
				if sample.GetCounter().GetValue() != 1 {
					t.Fatalf("pool timeouts: %v", sample)
				}
			case "erp_job_queue_depth":
				if sample.Label[0].GetValue() == "default" && sample.GetGauge().GetValue() != 1 {
					t.Fatalf("queue depth: %v", sample)
				}
			}
		}
		delete(expected, family.GetName())
	}
	if len(expected) != 0 {
		t.Fatalf("missing engine metric families: %v", expected)
	}
	if _, err := e.primaryDB.ExecContext(t.Context(), "UPDATE system.river_job SET state = 'running', attempted_at = now() WHERE kind = 'wasm_job'"); err != nil {
		t.Fatal(err)
	}
	if err := testutil.GatherAndCompare(e.metrics.registry, strings.NewReader(`
# HELP erp_job_queue_depth Queued jobs awaiting execution, including scheduled retries.
# TYPE erp_job_queue_depth gauge
erp_job_queue_depth{queue="critical"} 0
erp_job_queue_depth{queue="default"} 0
erp_job_queue_depth{queue="bulk"} 0
erp_job_queue_depth{queue="search"} 0
erp_job_queue_depth{queue="email"} 0
erp_job_queue_depth{queue="admin"} 0
erp_job_queue_depth{queue="events"} 0
`), "erp_job_queue_depth"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.moduleRegistry.UninstallModule(t.Context(), e.wasmRuntime, "metric_sales", nil, nil); err != nil {
		t.Fatal(err)
	}
	e.metrics.execution.Host("metric_sales", "host.time.now", "success", time.Millisecond)
	families, err = e.metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, sample := range family.Metric {
			for _, label := range sample.Label {
				if label.GetName() == "module" && label.GetValue() == "metric_sales" {
					t.Fatalf("uninstalled module series: %v", family)
				}
			}
		}
	}
}

func TestStateMetricsDatabaseFailure(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://unused:unused@localhost/unused")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(newStateCollector(db, &registry.ModuleRegistry{}))
	if _, err := reg.Gather(); err == nil {
		t.Fatal("failed database query produced a successful scrape")
	}
}
