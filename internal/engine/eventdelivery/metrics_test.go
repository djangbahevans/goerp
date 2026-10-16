package eventdelivery

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdbtest"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

func TestMetricsFanOutCountsEventOnce(t *testing.T) {
	w, tenants, conn, ctx := newTestWorker(t, testEventName, []manifest.EventSubscription{
		{Name: testEventName, Handler: "first", Async: true},
		{Name: testEventName, Handler: "second", Async: true},
	})
	tn := newTestTenant(t, tenants, conn, uniqueSlug(t))
	reg := prometheus.NewRegistry()
	w.Metrics = NewMetrics(reg)
	job := &river.Job[jobqueue.EventDeliveryArgs]{
		Args: jobqueue.EventDeliveryArgs{
			EventID:      uniqueEventID(t, conn),
			EventName:    testEventName,
			EventVersion: 1,
			TenantID:     tn.ID,
			EmittedAt:    time.Now(),
			Payload:      []byte{},
		},
	}

	modules := w.ModuleRegistry
	w.ModuleRegistry = &registry.ModuleRegistry{}
	if err := w.Work(ctx, job); err == nil {
		t.Fatal("expected missing registry snapshot error")
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}

	w.ModuleRegistry = modules

	for range 2 {
		if err := w.Work(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	if got := subscriberJobHandlers(t, conn, job.Args.EventID); len(got) != 2 {
		t.Fatalf("subscriber jobs = %v, want two", got)
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP erp_event_dispatched_total Event delivery fan-outs completed, counted once per event log entry.
# TYPE erp_event_dispatched_total counter
erp_event_dispatched_total{event_name="test.event.happened"} 1
`), "erp_event_dispatched_total"); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsSubscriberAttempts(t *testing.T) {
	for _, sync := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			wasm   []byte
			failed bool
		}{
			{"success", buildHandleEventConstStatusModule(0), false},
			{"retry", buildHandleEventConstStatusModule(1), true},
			{"permanent", buildHandleEventConstStatusModule(2), true},
			{"trap", handleEventTrapsModule, true},
		} {
			t.Run(fmt.Sprintf("sync=%v/%s", sync, tc.name), func(t *testing.T) {
				w := newTestSubscriberWorker(t, tc.wasm)
				reg := prometheus.NewRegistry()
				m := NewMetrics(reg)
				w.Metrics, w.Invoker.Metrics = m, m
				emittedAt := time.Now().Add(-2 * time.Minute)
				d := &SyncDispatcher{ModuleRegistry: w.ModuleRegistry, Invoker: w.Invoker, Metrics: m}

				for range 2 {
					if sync {
						payload, err := (event.Envelope{Name: testEventName, Version: 1, EmittedAt: emittedAt}).Marshal()
						if err != nil {
							t.Fatal(err)
						}

						status, err := d.DispatchSync(t.Context(), testEventModuleName, testHandlerName, payload)
						if (err != nil || status != 0) != tc.failed {
							t.Fatalf("dispatch = %d, %v", status, err)
						}
					} else {
						err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
							EventName: testEventName, EventVersion: 1, ModuleName: testEventModuleName,
							HandlerName: testHandlerName, EmittedAt: emittedAt,
						})
						if (err != nil) != tc.failed {
							t.Fatalf("delivery = %v", err)
						}
					}
				}

				families, err := reg.Gather()
				if err != nil {
					t.Fatal(err)
				}
				foundLatency, foundFailure := false, false
				for _, family := range families {
					for _, metric := range family.Metric {
						switch family.GetName() {
						case "erp_event_dispatch_latency_seconds":
							foundLatency = true
							if len(metric.Label) != 1 || metric.Label[0].GetName() != "event_name" {
								t.Fatalf("latency labels = %v", metric.Label)
							}
							h := metric.GetHistogram()
							if h.GetSampleCount() != 2 || h.GetSampleSum() < 240 || h.GetSampleSum() > 250 {
								t.Fatalf("latency = %v, want two observations of about 120 seconds", h)
							}
						case "erp_event_dispatch_failures_total":
							foundFailure = true
							if len(metric.Label) != 2 || metric.Label[0].GetName() != "event_name" || metric.Label[1].GetName() != "subscriber_module" || metric.GetCounter().GetValue() != 2 {
								t.Fatalf("failure metric = %v", metric)
							}
						}
					}
				}
				if !foundLatency || foundFailure != tc.failed {
					t.Fatalf("latency present = %v, failures present = %v", foundLatency, foundFailure)
				}
			})
		}
	}
}

func TestMetricsAdmissionFailureHasNoInvocationLatency(t *testing.T) {
	w := newTestSubscriberWorker(t, buildHandleEventConstStatusModule(0))
	reg := prometheus.NewRegistry()
	w.Metrics = NewMetrics(reg)
	w.Invoker.Metrics = w.Metrics
	if err := runSubscriberWork(t, w, jobqueue.SubscriberDeliveryArgs{
		EventName: testEventName, EventVersion: 2, ModuleName: testEventModuleName,
		HandlerName: testHandlerName, EmittedAt: time.Now().Add(-time.Hour),
	}); err == nil {
		t.Fatal("expected stale subscription error")
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP erp_event_dispatch_failures_total Failed subscriber delivery attempts, including admission failures.
# TYPE erp_event_dispatch_failures_total counter
erp_event_dispatch_failures_total{event_name="test.event.happened",subscriber_module="testmodule"} 1
`)); err != nil {
		t.Fatal(err)
	}
}

func TestQueueCollectorTracksRetryAndPurge(t *testing.T) {
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, localPostgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	driver := riverpgxv5.New(pool)
	schema := riverdbtest.TestSchema(ctx, t, driver, &riverdbtest.TestSchemaOpts{DisableReuse: true})
	client, err := river.NewClient(driver, &river.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	collector := newQueueCollector(conn, schema)
	reg := prometheus.NewRegistry()
	reg.MustRegister(collector)

	var ids []int64
	for i := range 3 {
		args := jobqueue.SubscriberDeliveryArgs{
			EventName: testEventName, ModuleName: testEventModuleName,
			TenantID: fmt.Sprintf("tenant-%d", i),
		}
		job, err := client.Insert(ctx, args, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.Job.ID)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE `+collector.table+` SET state='discarded', finalized_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Insert(ctx, jobqueue.EventDeliveryArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Insert(ctx, jobqueue.SubscriberDeliveryArgs{EventName: "ignored"}, &river.InsertOpts{Queue: jobqueue.QueueDefault}); err != nil {
		t.Fatal(err)
	}

	check := func(wantDLQ, wantAvailable int) {
		t.Helper()
		var expected strings.Builder
		expected.WriteString("# HELP river_job_count Stored jobs in the events queue by state.\n# TYPE river_job_count gauge\n")
		for _, state := range rivertype.JobStates() {
			count := 0
			switch state {
			case rivertype.JobStateAvailable:
				count = wantAvailable
			case rivertype.JobStateDiscarded:
				count = wantDLQ
			}
			fmt.Fprintf(&expected, "river_job_count{queue=\"events\",state=%q} %d\n", state, count)
		}
		if wantDLQ > 0 {
			expected.WriteString("# HELP erp_event_dlq_size Discarded subscriber delivery jobs.\n# TYPE erp_event_dlq_size gauge\n")
			fmt.Fprintf(&expected, "erp_event_dlq_size{event_name=%q,subscriber_module=%q} %d\n", testEventName, testEventModuleName, wantDLQ)
		}
		if err := testutil.GatherAndCompare(reg, strings.NewReader(expected.String())); err != nil {
			t.Fatal(err)
		}
	}
	check(3, 1)
	if _, err := client.JobRetry(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	check(2, 2)
	if _, err := conn.ExecContext(ctx, `DELETE FROM `+collector.table+` WHERE state='discarded'`); err != nil {
		t.Fatal(err)
	}
	check(0, 2)

	if _, err := conn.ExecContext(ctx, `DROP TABLE `+collector.table+` CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Gather(); err == nil {
		t.Fatal("failed collector query must fail the scrape")
	}
}

func TestMetricsRegistriesAreIsolated(t *testing.T) {
	first, second := prometheus.NewRegistry(), prometheus.NewRegistry()
	m := NewMetrics(first)
	NewMetrics(second)
	m.observeFailure(testEventName, testEventModuleName)
	if err := testutil.GatherAndCompare(second, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
}
