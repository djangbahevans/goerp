package engine

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/prometheus/client_golang/prometheus"
)

type stateCollector struct {
	db      *sql.DB
	modules *registry.ModuleRegistry
	pool    *prometheus.Desc
	queue   *prometheus.Desc
	tenants *prometheus.Desc
	status  *prometheus.Desc
}

func newStateCollector(db *sql.DB, modules *registry.ModuleRegistry) *stateCollector {
	return &stateCollector{
		db:      db,
		modules: modules,
		pool:    prometheus.NewDesc("erp_wasm_pool_size", "Live WASM instances in the published module pool.", []string{"module", "state"}, nil),
		queue:   prometheus.NewDesc("erp_job_queue_depth", "Queued jobs awaiting execution, including scheduled retries.", []string{"queue"}, nil),
		tenants: prometheus.NewDesc("erp_active_tenants_total", "Currently active tenants.", nil, nil),
		status:  prometheus.NewDesc("erp_module_status", "Published module status, with one active state per module.", []string{"module", "status"}, nil),
	}
}

func (c *stateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.pool
	ch <- c.queue
	ch <- c.tenants
	ch <- c.status
}

func (c *stateCollector) Collect(ch chan<- prometheus.Metric) {
	if snap := c.modules.Snapshot(); snap != nil {
		for name, mod := range snap.Modules() {
			for status := module.StatusPending; status <= module.StatusUnloaded; status++ {
				value := 0.0
				if mod.Status == status {
					value = 1
				}
				ch <- prometheus.MustNewConstMetric(c.status, prometheus.GaugeValue, value, name, status.String())
			}
			if mod.Pool != nil {
				ch <- prometheus.MustNewConstMetric(c.pool, prometheus.GaugeValue, float64(mod.Pool.IdleCount()), name, "idle")
				ch <- prometheus.MustNewConstMetric(c.pool, prometheus.GaugeValue, float64(mod.Pool.InUseCount()), name, "in_use")
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	queues := []string{jobqueue.QueueCritical, jobqueue.QueueDefault, jobqueue.QueueBulk, jobqueue.QueueSearch, jobqueue.QueueEmail, jobqueue.QueueAdmin, jobqueue.QueueEvents}
	rows, err := c.db.QueryContext(ctx, `
		SELECT '', count(*) FROM system.tenants WHERE status = 'active'
		UNION ALL
		SELECT queue, count(*) FROM system.river_job
		WHERE queue = ANY($1) AND state IN ('available', 'pending', 'retryable', 'scheduled') GROUP BY queue`, queues)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.queue, fmt.Errorf("collect engine state metrics: %w", err))
		return
	}
	defer rows.Close()

	depths := make(map[string]float64, len(queues))
	var tenants float64
	for rows.Next() {
		var queue string
		var count float64
		if err := rows.Scan(&queue, &count); err != nil {
			ch <- prometheus.NewInvalidMetric(c.queue, err)
			return
		}
		if queue == "" {
			tenants = count
		} else {
			depths[queue] = count
		}
	}
	if err := rows.Err(); err != nil {
		ch <- prometheus.NewInvalidMetric(c.queue, err)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.tenants, prometheus.GaugeValue, tenants)
	for _, queue := range queues {
		ch <- prometheus.MustNewConstMetric(c.queue, prometheus.GaugeValue, depths[queue], queue)
	}
}
