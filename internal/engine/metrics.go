package engine

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type engineMetrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newEngineMetrics() *engineMetrics {
	m := &engineMetrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "erp_http_requests_total",
			Help: "HTTP requests served by the public engine listener.",
		}, []string{"method", "route", "module", "status_code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "erp_http_request_duration_seconds",
			Help: "HTTP request duration on the public engine listener in seconds.",
		}, []string{"method", "route", "module"}),
	}
	m.registry.MustRegister(m.requests, m.duration)

	return m
}

func (m *engineMetrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

type requestMetricsKey struct{}

// The initial resolution survives request cloning and SSE poll re-resolution, so
// observation outside recovery retains the admitted request's labels.
type requestMetrics struct {
	resolution *routeResolution
}

func (m *engineMetrics) middleware(next http.Handler) http.Handler {
	if m == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		labels := &requestMetrics{}
		ctx := context.WithValue(r.Context(), requestMetricsKey{}, labels)
		rec := &statusRecordingWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		path, module := "unmatched", ""
		if rr := labels.resolution; rr != nil {
			path, module = rr.entry.PathTemplate, rr.entry.ModuleName
		}

		method := metricMethod(r.Method)
		m.requests.WithLabelValues(method, path, module, strconv.Itoa(rec.status)).Inc()
		m.duration.WithLabelValues(method, path, module).Observe(time.Since(start).Seconds())
	})
}

func metricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "other"
	}
}
