package wasm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/tetratelabs/wazero/api"
)

func TestHostMetricsWithoutTracing(t *testing.T) {
	r := newHostcallTestRuntime(t, nil, 10)
	reg := prometheus.NewRegistry()
	r.metrics = metric.New(reg)
	inst := newLogCaller(t, r)
	inst.SetModuleContext(NewModuleContext("request", "sales", "", "", nil, nil, "tenant", "", "", 0, nil, ModuleSnapshot{}))
	for _, level := range []string{"info", "invalid"} {
		callHost(t, t.Context(), inst, "call_log", abiv1.LogInput{Level: level})
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP erp_host_function_calls_total Host function calls by outcome.
# TYPE erp_host_function_calls_total counter
erp_host_function_calls_total{function="host.log.log",module="sales",status="success"} 1
erp_host_function_calls_total{function="host.log.log",module="sales",status="error"} 1
`), "erp_host_function_calls_total"); err != nil {
		t.Fatal(err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "erp_host_function_duration_seconds" && family.Metric[0].GetHistogram().GetSampleCount() != 2 {
			t.Fatalf("host duration count: %v", family)
		}
	}
}

func TestHostPanicMetrics(t *testing.T) {
	r := newTestRuntime(t, 1<<20)
	reg := prometheus.NewRegistry()
	r.metrics = metric.New(reg)
	_, err := r.guardedHostModule(r.wazero, "host.metrics").NewFunctionBuilder().WithFunc(func(context.Context, api.Module, uint32, uint32) uint64 {
		panic("host failure")
	}).Export("explode").Instantiate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := r.CompileModule(t.Context(), buildHostCallerModule("host.metrics", []string{"explode"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	inst, err := newModuleInstance(t.Context(), "panic-metrics", compiled, r)
	if err != nil {
		t.Fatal(err)
	}
	inst.SetModuleContext(NewModuleContext("request", "sales", "", "", nil, nil, "tenant", "", "", 0, nil, ModuleSnapshot{}))
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })
	if _, err := inst.module.ExportedFunction("call_explode").Call(t.Context(), 0, 0); err == nil {
		t.Fatal("host panic succeeded")
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP erp_host_function_calls_total Host function calls by outcome.
# TYPE erp_host_function_calls_total counter
erp_host_function_calls_total{function="host.metrics.explode",module="sales",status="panic"} 1
`), "erp_host_function_calls_total"); err != nil {
		t.Fatal(err)
	}
}

func TestPoolTimeoutAndCompilationMetrics(t *testing.T) {
	r := newTestRuntime(t, 1<<20)
	reg := prometheus.NewRegistry()
	r.metrics = metric.New(reg)
	ctx := metric.WithModule(t.Context(), "sales")
	compiled, err := r.CompileModule(ctx, emptyModule)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	if _, err := r.CompileModule(ctx, []byte("invalid")); err == nil {
		t.Fatal("invalid WASM compiled")
	}
	pool, _ := newPool("sales", compiled, r, PoolConfig{MaxSize: 1, BorrowTimeout: time.Millisecond})
	inst, err := pool.Borrow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Return(inst) })
	if pool.InUseCount() != 1 {
		t.Fatal("borrowed instance absent from live pool count")
	}
	if _, err := pool.Borrow(t.Context()); !errors.Is(err, ErrPoolTimeout) {
		t.Fatalf("borrow = %v", err)
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP erp_wasm_pool_borrow_timeouts_total WASM instance acquisitions that time out or are cancelled.
# TYPE erp_wasm_pool_borrow_timeouts_total counter
erp_wasm_pool_borrow_timeouts_total{module="sales"} 1
`), "erp_wasm_pool_borrow_timeouts_total"); err != nil {
		t.Fatal(err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "erp_wasm_compilation_duration_seconds" && family.Metric[0].GetHistogram().GetSampleCount() != 2 {
			t.Fatalf("compilation count: %v", family)
		}
	}
}
