package eventdelivery

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river/rivertype"
)

type Metrics struct {
	dispatched *prometheus.CounterVec
	failures   *prometheus.CounterVec
	latency    *prometheus.HistogramVec
}

func NewMetrics(registerer prometheus.Registerer) *Metrics {
	m := &Metrics{
		dispatched: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "erp_event_dispatched_total",
			Help: "Event delivery fan-outs completed, counted once per event log entry.",
		}, []string{"event_name"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "erp_event_dispatch_failures_total",
			Help: "Failed subscriber delivery attempts, including admission failures.",
		}, []string{"event_name", "subscriber_module"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "erp_event_dispatch_latency_seconds",
			Help:    "Time from event emission to subscriber invocation in seconds.",
			Buckets: []float64{0.01, 0.1, 0.5, 1, 5, 10, 30, 60, 300, 1800, 3600},
		}, []string{"event_name"}),
	}
	registerer.MustRegister(m.dispatched, m.failures, m.latency)

	return m
}

func (m *Metrics) observeFailure(eventName, moduleName string) {
	if m != nil {
		m.failures.WithLabelValues(eventName, moduleName).Inc()
	}
}

func (m *Metrics) observeInvocation(eventName string, emittedAt time.Time) {
	if m != nil && !emittedAt.IsZero() {
		m.latency.WithLabelValues(eventName).Observe(max(0, time.Since(emittedAt).Seconds()))
	}
}

type QueueCollector struct {
	pool  *sql.DB
	table string
	depth *prometheus.Desc
	dlq   *prometheus.Desc
}

func NewQueueCollector(pool *sql.DB) *QueueCollector {
	return newQueueCollector(pool, jobqueue.Schema)
}

func newQueueCollector(pool *sql.DB, schema string) *QueueCollector {
	return &QueueCollector{
		pool:  pool,
		table: pgx.Identifier{schema, "river_job"}.Sanitize(),
		depth: prometheus.NewDesc("river_job_count", "Stored jobs in the events queue by state.", []string{"queue", "state"}, nil),
		dlq:   prometheus.NewDesc("erp_event_dlq_size", "Discarded subscriber delivery jobs.", []string{"event_name", "subscriber_module"}, nil),
	}
}

func (c *QueueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.depth
	ch <- c.dlq
}

func (c *QueueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Both gauges use one database snapshot; a failed query must not report a healthy zero.
	rows, err := c.pool.QueryContext(ctx, `
		SELECT 'queue', state::text, '', '', count(*) FROM `+c.table+`
		WHERE queue = $1 GROUP BY state
		UNION ALL
		SELECT 'dlq', '', COALESCE(args->>'event_name', ''), COALESCE(args->>'module_name', ''), count(*)
		FROM `+c.table+` WHERE kind = 'subscriber_delivery' AND state = 'discarded'
		GROUP BY args->>'event_name', args->>'module_name'`, jobqueue.QueueEvents)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.depth, fmt.Errorf("collect event queue metrics: %w", err))
		return
	}
	defer rows.Close()

	depths := make(map[string]float64)
	var dlq []prometheus.Metric
	for rows.Next() {
		var kind, state, eventName, moduleName string
		var count float64
		if err := rows.Scan(&kind, &state, &eventName, &moduleName, &count); err != nil {
			ch <- prometheus.NewInvalidMetric(c.depth, fmt.Errorf("scan event queue metrics: %w", err))
			return
		}

		if kind == "queue" {
			depths[state] = count
		} else {
			dlq = append(dlq, prometheus.MustNewConstMetric(c.dlq, prometheus.GaugeValue, count, eventName, moduleName))
		}
	}
	if err := rows.Err(); err != nil {
		ch <- prometheus.NewInvalidMetric(c.depth, fmt.Errorf("read event queue metrics: %w", err))
		return
	}

	for _, state := range rivertype.JobStates() {
		ch <- prometheus.MustNewConstMetric(c.depth, prometheus.GaugeValue, depths[string(state)], jobqueue.QueueEvents, string(state))
	}
	for _, metric := range dlq {
		ch <- metric
	}
}
