// Package metric records bounded engine execution metrics and module-series lifetimes.
package metric

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	mu             sync.RWMutex
	removed        map[string]bool
	borrow         *prometheus.HistogramVec
	timeouts       *prometheus.CounterVec
	compilation    *prometheus.HistogramVec
	hostCalls      *prometheus.CounterVec
	hostDuration   *prometheus.HistogramVec
	query          *prometheus.HistogramVec
	jobs           *prometheus.CounterVec
	jobDuration    *prometheus.HistogramVec
	syncDuration   *prometheus.HistogramVec
	syncOperations *prometheus.CounterVec
}

func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		removed:        make(map[string]bool),
		borrow:         histogram("erp_wasm_pool_borrow_duration_seconds", "Time spent acquiring a WASM instance in seconds, including instantiation, for successful borrows.", "module"),
		timeouts:       counter("erp_wasm_pool_borrow_timeouts_total", "WASM instance acquisitions that time out or are cancelled.", "module"),
		compilation:    histogram("erp_wasm_compilation_duration_seconds", "WASM compilation attempts in seconds, including cache lookup.", "module"),
		hostCalls:      counter("erp_host_function_calls_total", "Host function calls by outcome.", "function", "module", "status"),
		hostDuration:   histogram("erp_host_function_duration_seconds", "Host function call duration in seconds.", "function", "module"),
		query:          histogram("erp_db_query_duration_seconds", "Database statement duration in seconds.", "module", "operation"),
		jobs:           counter("erp_job_executed_total", "Job execution attempts by outcome.", "job_type", "module", "status"),
		jobDuration:    histogram("erp_job_duration_seconds", "Job execution attempt duration in seconds.", "job_type", "module"),
		syncDuration:   histogram("erp_schema_sync_duration_seconds", "Tenant module schema synchronization duration in seconds.", "module"),
		syncOperations: counter("erp_schema_sync_operations_total", "Applied schema changes by operation type.", "module", "operation_type"),
	}
	reg.MustRegister(m.borrow, m.timeouts, m.compilation, m.hostCalls, m.hostDuration, m.query, m.jobs, m.jobDuration, m.syncDuration, m.syncOperations)

	return m
}

func histogram(name, help string, labels ...string) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help}, labels)
}

func counter(name, help string, labels ...string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
}

// Activate permits observations after a module is installed again.
func (m *Metrics) Activate(module string) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.removed, module)
}

// Remove prevents in-flight work from recreating an uninstalled module's series.
func (m *Metrics) Remove(module string, cleanup ...func()) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed[module] = true
	for _, fn := range cleanup {
		fn()
	}
	labels := prometheus.Labels{"module": module}
	for _, v := range []*prometheus.HistogramVec{m.borrow, m.compilation, m.hostDuration, m.query, m.jobDuration, m.syncDuration} {
		v.DeletePartialMatch(labels)
	}
	for _, v := range []*prometheus.CounterVec{m.timeouts, m.hostCalls, m.jobs, m.syncOperations} {
		v.DeletePartialMatch(labels)
	}
}

// Record serializes observations with module removal.
func (m *Metrics) Record(module string, fn func()) {
	if m == nil {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.removed[module] {
		fn()
	}
}

func (m *Metrics) BorrowHistogram(module string) prometheus.Histogram {
	var histogram prometheus.Histogram
	m.Record(module, func() {
		m.timeouts.WithLabelValues(module).Add(0)
		histogram = m.borrow.WithLabelValues(module).(prometheus.Histogram)
	})
	if histogram == nil {
		// A pool prepared before removal may finish construction after publication.
		return prometheus.NewHistogram(prometheus.HistogramOpts{Name: "erp_wasm_pool_borrow_duration_seconds"})
	}

	return histogram
}

func (m *Metrics) BorrowTimeout(module string) {
	m.Record(module, func() { m.timeouts.WithLabelValues(module).Inc() })
}

func (m *Metrics) Compilation(module string, duration time.Duration) {
	m.Record(module, func() { m.compilation.WithLabelValues(module).Observe(duration.Seconds()) })
}

func (m *Metrics) Host(module, function, status string, duration time.Duration) {
	m.Record(module, func() {
		m.hostCalls.WithLabelValues(function, module, status).Inc()
		m.hostDuration.WithLabelValues(function, module).Observe(duration.Seconds())
	})
}

func (m *Metrics) Query(module, operation string, duration time.Duration) {
	switch operation {
	case "select", "insert", "update", "delete":
		m.Record(module, func() { m.query.WithLabelValues(module, operation).Observe(duration.Seconds()) })
	}
}

func (m *Metrics) Job(module, jobType, status string, duration time.Duration) {
	m.Record(module, func() {
		m.jobs.WithLabelValues(jobType, module, status).Inc()
		m.jobDuration.WithLabelValues(jobType, module).Observe(duration.Seconds())
	})
}

func (m *Metrics) Sync(module string, duration time.Duration) {
	m.Record(module, func() { m.syncDuration.WithLabelValues(module).Observe(duration.Seconds()) })
}

func (m *Metrics) SchemaOperation(module, operation string) {
	m.Record(module, func() { m.syncOperations.WithLabelValues(module, operation).Inc() })
}

type moduleKey struct{}

// WithModule carries an engine-resolved module name to database instrumentation.
func WithModule(ctx context.Context, module string) context.Context {
	return context.WithValue(ctx, moduleKey{}, module)
}

func Module(ctx context.Context) string {
	module, _ := ctx.Value(moduleKey{}).(string)
	return module
}
