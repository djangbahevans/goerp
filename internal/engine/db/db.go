// Package db opens the engine's Postgres connection pools (primary and
// optionally replica), pinging eagerly so a Stage 1 connectivity failure
// surfaces immediately at startup rather than on first query.
package db

import (
	"database/sql"
	"fmt"

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
