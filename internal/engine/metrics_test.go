package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestHTTPMetricsRouteCardinality(t *testing.T) {
	m := newEngineMetrics()
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{}); err != nil {
		t.Fatal(err)
	}
	reg.Snapshot().RouteTable().Register(http.MethodGet, "/contacts/{id}", &route.RouteEntry{
		PathTemplate: "/contacts/{id}",
		ModuleName:   "contacts",
	})

	h := m.middleware(recoveryMiddleware()(routeResolutionMiddleware(reg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := metric.Module(r.Context()); got != "contacts" {
			t.Errorf("database metric module = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))))
	for _, request := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/contacts/123", http.StatusNoContent},
		{http.MethodGet, "/contacts/456", http.StatusNoContent},
		{http.MethodGet, "/unknown/123", http.StatusNotFound},
		{http.MethodGet, "/unknown/456", http.StatusNotFound},
		{http.MethodPost, "/contacts/123", http.StatusMethodNotAllowed},
		{http.MethodGet, "/contacts/../invalid", http.StatusBadRequest},
		{"CUSTOM_ONE", "/unknown", http.StatusNotFound},
		{"CUSTOM_TWO", "/unknown", http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(request.method, request.path, nil))
		if w.Code != request.status {
			t.Fatalf("%s %s = %d, want %d", request.method, request.path, w.Code, request.status)
		}
	}

	if err := testutil.GatherAndCompare(m.registry, strings.NewReader(`
# HELP erp_http_requests_total HTTP requests served by the public engine listener.
# TYPE erp_http_requests_total counter
erp_http_requests_total{method="GET",module="contacts",route="/contacts/{id}",status_code="204"} 2
erp_http_requests_total{method="GET",module="",route="unmatched",status_code="400"} 1
erp_http_requests_total{method="GET",module="",route="unmatched",status_code="404"} 2
erp_http_requests_total{method="POST",module="",route="unmatched",status_code="405"} 1
erp_http_requests_total{method="other",module="",route="unmatched",status_code="404"} 2
`), "erp_http_requests_total"); err != nil {
		t.Fatal(err)
	}

	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	foundDuration := false
	for _, family := range families {
		if family.GetName() != "erp_http_request_duration_seconds" {
			continue
		}
		foundDuration = true
		if len(family.Metric) != 4 {
			t.Fatalf("duration series = %d, want 4", len(family.Metric))
		}
		var count uint64
		for _, metric := range family.Metric {
			if len(metric.Label) != 3 {
				t.Fatalf("duration labels = %v, want method, route, module", metric.Label)
			}
			count += metric.GetHistogram().GetSampleCount()
		}
		if count != 8 {
			t.Errorf("duration observations = %d, want 8", count)
		}
	}
	if !foundDuration {
		t.Fatal("request duration histogram is missing")
	}
}

func TestHTTPMetricsResponseStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		serve  http.HandlerFunc
		status string
	}{
		{
			name: "implicit success",
			serve: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("ok"))
			},
			status: "200",
		},
		{
			name: "authentication rejection",
			serve: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			status: "401",
		},
		{
			name: "panic",
			serve: func(http.ResponseWriter, *http.Request) {
				panic("test panic")
			},
			status: "500",
		},
		{
			name: "informational response",
			serve: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusEarlyHints)
				w.WriteHeader(http.StatusAccepted)
			},
			status: "202",
		},
		{
			name: "flush commits success",
			serve: func(w http.ResponseWriter, _ *http.Request) {
				_ = http.NewResponseController(w).Flush()
				w.WriteHeader(http.StatusInternalServerError)
			},
			status: "200",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newEngineMetrics()
			reg := &registry.ModuleRegistry{}
			if _, err := reg.Update(map[string]*module.LoadedModule{}); err != nil {
				t.Fatal(err)
			}
			h := m.middleware(recoveryMiddleware()(routeResolutionMiddleware(reg)(otelMiddleware(noop.NewTracerProvider().Tracer("test"))(tc.serve))))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/_health", nil))

			if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "/_health", "", tc.status)); got != 1 {
				t.Errorf("requests with status %s = %v, want 1", tc.status, got)
			}
		})
	}
}

func TestHTTPMetricsUnavailableRegistry(t *testing.T) {
	m := newEngineMetrics()
	h := m.middleware(routeResolutionMiddleware(&registry.ModuleRegistry{})(http.NotFoundHandler()))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/contacts/123", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "unmatched", "", "503")); got != 1 {
		t.Errorf("unresolved requests = %v, want 1", got)
	}
}

func TestHTTPMetricsRetainsInitialResolution(t *testing.T) {
	m := newEngineMetrics()
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{}); err != nil {
		t.Fatal(err)
	}
	table := reg.Snapshot().RouteTable()
	table.Register(http.MethodGet, "/contacts/{id}", &route.RouteEntry{
		PathTemplate: "/contacts/{id}",
		ModuleName:   "contacts",
	})
	h := m.middleware(routeResolutionMiddleware(reg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		table.Register(http.MethodGet, "/contacts/123", &route.RouteEntry{
			PathTemplate: "/contacts/123",
			ModuleName:   "replacement",
		})
		routeResolutionMiddleware(reg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(w, r.Clone(r.Context()))
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/contacts/123", nil))

	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "/contacts/{id}", "contacts", "204")); got != 1 {
		t.Errorf("requests using initial resolution = %v, want 1", got)
	}
}

func TestHTTPMetricsWebSocketUpgrade(t *testing.T) {
	m := newEngineMetrics()
	done := make(chan struct{})
	h := m.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.CloseNow()
	}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		close(done)
	}))
	t.Cleanup(server.Close)

	conn, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.CloseNow()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("upgrade handler did not finish")
	}

	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "unmatched", "", "101")); got != 1 {
		t.Errorf("upgrade requests = %v, want 1", got)
	}
}

func TestMetricsEndpointEngineIsolation(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	var engines []*Engine
	for range 2 {
		cfg := baseTestConfig(t)
		cfg.ModuleDev = true
		cfg.ModuleDir = t.TempDir()
		cfg.MetricsEnabled = true
		cfg.AdminMaxConcurrent = 10
		cfg.ListenAddr = shutdownTestAddress(t)
		cfg.AdminAddr = shutdownTestAddress(t)
		e, err := New(cfg)
		requireEngineConstruction(t, err)
		closeTestEnginePools(t, e)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := e.Shutdown(ctx); err != nil {
				t.Errorf("shutdown engine: %v", err)
			}
		})
		if err := e.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		engines = append(engines, e)
	}

	first := engines[0]
	requestBody := func(addr, path string, status int) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, body)
		}
		if path == "/_metrics" && status == http.StatusOK && !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain;") {
			t.Errorf("metrics content type = %q, want Prometheus text", resp.Header.Get("Content-Type"))
		}
		return string(body)
	}
	requestBody(first.cfg.ListenAddr, "/_ready", http.StatusOK)
	requestBody(first.cfg.ListenAddr, "/_metrics", http.StatusNotFound)

	compiled, err := first.wasmRuntime.CompileModule(t.Context(), []byte{0, 0x61, 0x73, 0x6d, 1, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	pool := first.wasmRuntime.NewPool("metricsfixture", compiled, wasm.PoolConfig{
		WarmSize: 1,
		MaxSize:  1,
	})
	t.Cleanup(func() {
		_ = pool.DrainAndClose(context.Background(), time.Second)
		_ = compiled.Close(context.Background())
	})
	inst, err := pool.Borrow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pool.Return(inst)

	body := requestBody(first.cfg.AdminAddr, "/_metrics", http.StatusOK)
	for _, expected := range []string{
		`erp_http_requests_total{method="GET",module="",route="/_ready",status_code="200"} 1`,
		`erp_http_request_duration_seconds_count{method="GET",module="",route="/_ready"} 1`,
		`erp_wasm_pool_borrow_duration_seconds_count{module="metricsfixture"} 1`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("scrape missing %s: %s", expected, body)
		}
	}
	if second := requestBody(engines[1].cfg.AdminAddr, "/_metrics", http.StatusOK); strings.Contains(second, "erp_http_requests_total{") || strings.Contains(second, "metricsfixture") {
		t.Errorf("second engine shares metrics with first: %s", second)
	}
}

func TestMetricsEndpointDisabled(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.ModuleDev = true
	cfg.ModuleDir = t.TempDir()
	cfg.MetricsEnabled = false
	e, err := New(cfg)
	requireEngineConstruction(t, err)
	closeTestEnginePools(t, e)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Shutdown(ctx); err != nil {
			t.Errorf("shutdown engine: %v", err)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/_metrics", nil)
	req.Header.Set("Authorization", "Bearer test-admin-token")
	w := httptest.NewRecorder()
	e.adminServer.UnauthenticatedRouter().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled scrape status = %d, want 404: %s", w.Code, w.Body.String())
	}
}

func TestEventQueueMetricsEngineRegistry(t *testing.T) {
	cfg := baseTestConfig(t)
	cfg.ModuleDev = true
	cfg.ModuleDir = t.TempDir()
	cfg.MetricsEnabled = true
	e, err := New(cfg)
	requireEngineConstruction(t, err)
	closeTestEnginePools(t, e)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Shutdown(ctx); err != nil {
			t.Errorf("shutdown engine: %v", err)
		}
	})

	job, err := e.jobQueue.Insert(t.Context(), jobqueue.SubscriberDeliveryArgs{
		EventName: "sales.confirmed", ModuleName: "inventory", TenantID: "tenant-a",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.primaryDB.ExecContext(t.Context(), `UPDATE system.river_job SET state='discarded', finalized_at=now() WHERE id=$1`, job.Job.ID); err != nil {
		t.Fatal(err)
	}

	if err := testutil.GatherAndCompare(e.metrics.registry, strings.NewReader(`
# HELP erp_event_dlq_size Discarded subscriber delivery jobs.
# TYPE erp_event_dlq_size gauge
erp_event_dlq_size{event_name="sales.confirmed",subscriber_module="inventory"} 1
`), "erp_event_dlq_size"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	e.adminServer.UnauthenticatedRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/_metrics", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `river_job_count{queue="events",state="discarded"} 1`) {
		t.Fatalf("scrape = %d: %s", w.Code, w.Body.String())
	}

	if _, err := e.jobQueue.JobRetry(t.Context(), job.Job.ID); err != nil {
		t.Fatal(err)
	}
	if err := testutil.GatherAndCompare(e.metrics.registry, strings.NewReader(""), "erp_event_dlq_size"); err != nil {
		t.Fatal(err)
	}
}
