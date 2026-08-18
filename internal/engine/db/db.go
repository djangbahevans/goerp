// Package db opens the engine's Postgres connection pools (primary and
// optionally replica), pinging eagerly so a Stage 1 connectivity failure
// surfaces immediately at startup rather than on first query.
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func New(url string) (*sql.DB, error) {
	pool, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}

// NewPgxPool opens a native pgx connection pool, for callers that need
// pgx's own types directly (currently just jobqueue.New/jobqueue.Migrate,
// via river's riverpgxv5 driver) rather than the database/sql-wrapped pool
// New returns. Pings eagerly, same as New.
func NewPgxPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("create pgx connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
