package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/metric"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDatabaseQueryMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metric.New(reg)
	pool, err := db.New("postgres://goerp:dev@localhost:6432/goerp", db.Metrics{Registerer: reg, Execution: m, Pool: "primary"})
	if err != nil {
		t.Skipf("dev Postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	ctx := metric.WithModule(t.Context(), "sales")
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	for _, statement := range []string{
		"CREATE TEMP TABLE metric_queries (id integer) ON COMMIT DROP",
		"INSERT INTO metric_queries VALUES (1)",
		"/* comment */ WITH ids AS (SELECT id FROM metric_queries) SELECT * FROM ids",
		"UPDATE metric_queries SET id = 2",
		"DELETE FROM metric_queries",
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, "SELECT 1/0"); err == nil {
		t.Fatal("division by zero succeeded")
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, family := range families {
		if family.GetName() != "erp_db_query_duration_seconds" {
			continue
		}
		seen = true
		if len(family.Metric) != 4 {
			t.Fatalf("query series: %v", family)
		}
		for _, sample := range family.Metric {
			var module, operation string
			for _, label := range sample.Label {
				if label.GetName() == "module" {
					module = label.GetValue()
				}
				if label.GetName() == "operation" {
					operation = label.GetValue()
				}
			}
			want := uint64(1)
			if operation == "select" {
				want = 2
			}
			if module != "sales" || sample.GetHistogram().GetSampleCount() != want {
				t.Fatalf("query observation: %v", sample)
			}
		}
	}
	if !seen {
		t.Fatal("query histogram missing")
	}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP erp_db_pool_connections Database connections by pool and state; waiting includes pending connection acquisition.
# TYPE erp_db_pool_connections gauge
erp_db_pool_connections{pool="primary",state="idle"} 0
erp_db_pool_connections{pool="primary",state="in_use"} 1
erp_db_pool_connections{pool="primary",state="waiting"} 0
`), "erp_db_pool_connections"); err != nil {
		t.Fatal(err)
	}
}

func TestBatchAndCopyMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	pool, err := db.NewPgxPool(t.Context(), "postgres://goerp:dev@localhost:6432/goerp", db.Metrics{Registerer: reg, Execution: metric.New(reg), Pool: "jobs"})
	if err != nil {
		t.Skipf("dev Postgres unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx := metric.WithModule(t.Context(), "sales")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, "CREATE TEMP TABLE metric_batch (id integer) ON COMMIT DROP"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"metric_batch"}, []string{"id"}, pgx.CopyFromRows([][]any{{1}, {2}})); err != nil {
		t.Fatal(err)
	}
	batch := &pgx.Batch{}
	batch.Queue("INSERT INTO metric_batch VALUES (3)")
	batch.Queue("UPDATE metric_batch SET id = id+1")
	batch.Queue("DELETE FROM metric_batch WHERE id = 2")
	results := tx.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "erp_db_query_duration_seconds" {
			continue
		}
		if len(family.Metric) != 3 {
			t.Fatalf("batch and copy series: %v", family)
		}
		for _, sample := range family.Metric {
			want := uint64(1)
			for _, label := range sample.Label {
				if label.GetName() == "operation" && label.GetValue() == "insert" {
					want = 2
				}
			}
			if sample.GetHistogram().GetSampleCount() != want {
				t.Fatalf("batch and copy observation: %v", sample)
			}
		}
		return
	}
	t.Fatal("batch and copy histogram missing")
}

func TestNativePoolWaitingGauge(t *testing.T) {
	reg := prometheus.NewRegistry()
	pool, err := db.NewPgxPool(t.Context(), "postgres://goerp:dev@localhost:6432/goerp?pool_max_conns=1", db.Metrics{Registerer: reg, Execution: metric.New(reg), Pool: "jobs"})
	if err != nil {
		t.Skipf("dev Postgres unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		conn, err := pool.Acquire(ctx)
		if err == nil {
			conn.Release()
		}
		done <- err
	}()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(5 * time.Second)
	for {
		families, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		waiting := false
		for _, family := range families {
			if family.GetName() != "erp_db_pool_connections" {
				continue
			}
			for _, sample := range family.Metric {
				for _, label := range sample.Label {
					if label.GetName() == "state" && label.GetValue() == "waiting" && sample.GetGauge().GetValue() == 1 {
						waiting = true
					}
				}
			}
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("blocked acquire absent from waiting gauge")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
}
