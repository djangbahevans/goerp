package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync/atomic"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/prometheus/client_golang/prometheus"
	pgquery "github.com/wasilibs/go-pgquery"
)

type Metrics struct {
	Registerer prometheus.Registerer
	Execution  *metric.Metrics
	Pool       string
}

type queryStartKey struct{}

type queryObservation struct {
	start time.Time
	sql   string
}

type queryTracer struct {
	metrics   *metric.Metrics
	acquiring atomic.Int64
}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryStartKey{}, queryObservation{start: time.Now(), sql: data.SQL})
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	observation, ok := ctx.Value(queryStartKey{}).(queryObservation)
	if !ok {
		return
	}
	t.recordQuery(ctx, observation, data.CommandTag, data.Err != nil)
}

func (t *queryTracer) recordQuery(ctx context.Context, observation queryObservation, tag pgconn.CommandTag, failed bool) {
	duration := time.Since(observation.start)
	operation := ""
	switch {
	case tag.Select():
		operation = "select"
	case tag.Insert():
		operation = "insert"
	case tag.Update():
		operation = "update"
	case tag.Delete():
		operation = "delete"
	default:
		// Failed statements have no command tag; parse only that exceptional path.
		if !failed {
			return
		}
		tree, err := pgquery.Parse(observation.sql)
		if err == nil && len(tree.Stmts) == 1 {
			switch tree.Stmts[0].Stmt.GetNode().(type) {
			case *pg_query.Node_SelectStmt:
				operation = "select"
			case *pg_query.Node_InsertStmt:
				operation = "insert"
			case *pg_query.Node_UpdateStmt:
				operation = "update"
			case *pg_query.Node_DeleteStmt:
				operation = "delete"
			}
		}
	}
	t.metrics.Query(metric.Module(ctx), operation, duration)
}

func (t *queryTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return context.WithValue(ctx, queryStartKey{}, &queryObservation{start: time.Now()})
}

func (t *queryTracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	observation, ok := ctx.Value(queryStartKey{}).(*queryObservation)
	if !ok {
		return
	}
	observation.sql = data.SQL
	t.recordQuery(ctx, *observation, data.CommandTag, data.Err != nil)
	observation.start = time.Now()
}

func (t *queryTracer) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

func (t *queryTracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	return context.WithValue(ctx, queryStartKey{}, time.Now())
}

func (t *queryTracer) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromEndData) {
	if start, ok := ctx.Value(queryStartKey{}).(time.Time); ok {
		t.metrics.Query(metric.Module(ctx), "insert", time.Since(start))
	}
}

func (t *queryTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	t.acquiring.Add(1)
	return ctx
}

func (t *queryTracer) TraceAcquireEnd(context.Context, *pgxpool.Pool, pgxpool.TraceAcquireEndData) {
	t.acquiring.Add(-1)
}

type poolConnector struct {
	driver.Connector
	tracer *queryTracer
}

func (c *poolConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.tracer.acquiring.Add(1)
	defer c.tracer.acquiring.Add(-1)
	return c.Connector.Connect(ctx)
}

func registerSQLPool(cfg Metrics, pool *sql.DB, tracer *queryTracer) {
	registerPool(cfg, func() (float64, float64, float64) {
		stats := pool.Stats()
		return float64(stats.Idle), float64(stats.InUse), float64(tracer.acquiring.Load())
	})
}

func registerPool(cfg Metrics, stats func() (float64, float64, float64)) {
	if cfg.Registerer == nil {
		return
	}
	cfg.Registerer.MustRegister(&poolCollector{
		desc:  prometheus.NewDesc("erp_db_pool_connections", "Database connections by pool and state; waiting includes pending connection acquisition.", []string{"state"}, prometheus.Labels{"pool": cfg.Pool}),
		stats: stats,
	})
}

type poolCollector struct {
	desc  *prometheus.Desc
	stats func() (float64, float64, float64)
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	idle, inUse, waiting := c.stats()
	for state, value := range map[string]float64{"idle": idle, "in_use": inUse, "waiting": waiting} {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, value, state)
	}
}

// poolName is restricted to engine-owned pool roles rather than DSNs.
func poolName(name string) string {
	switch strings.ToLower(name) {
	case "primary", "replica", "schema_sync", "jobs":
		return strings.ToLower(name)
	default:
		return "other"
	}
}
