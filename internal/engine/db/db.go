// Package db opens the engine's Postgres connection pools (primary and
// optionally replica), pinging eagerly so a Stage 1 connectivity failure
// surfaces immediately at startup rather than on first query.
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

func New(url string, metrics ...Metrics) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	tracer := &queryTracer{}
	if len(metrics) > 0 {
		tracer.metrics = metrics[0].Execution
		cfg.Tracer = tracer
	}
	connector := stdlib.GetConnector(*cfg)
	if len(metrics) > 0 {
		connector = &poolConnector{Connector: connector, tracer: tracer}
	}
	pool := sql.OpenDB(connector)

	if err := pool.Ping(); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	if len(metrics) > 0 {
		cfg := metrics[0]
		cfg.Pool = poolName(cfg.Pool)
		registerSQLPool(cfg, pool, tracer)
	}

	return pool, nil
}

// NewPgxPool opens and pings a native pgx pool for River clients.
func NewPgxPool(ctx context.Context, url string, metrics ...Metrics) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse pool configuration: %w", err)
	}
	tracer := &queryTracer{}
	if len(metrics) > 0 {
		tracer.metrics = metrics[0].Execution
		cfg.ConnConfig.Tracer = tracer
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pgx connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	if len(metrics) > 0 {
		c := metrics[0]
		c.Pool = poolName(c.Pool)
		registerPool(c, func() (float64, float64, float64) {
			stats := pool.Stat()
			return float64(stats.IdleConns()), float64(stats.AcquiredConns()), float64(tracer.acquiring.Load())
		})
	}

	return pool, nil
}
