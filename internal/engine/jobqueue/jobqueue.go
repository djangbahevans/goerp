// Package jobqueue is the engine's River-backed background job queue
// (erp-design.md §7.1): 5 named queues, per-queue concurrency, and the
// idempotency-key convention job types should follow (see ProbeArgs).
// Wiring into engine.New and real job types belongs to later tickets.
package jobqueue

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// Queue names from erp-design.md §7.1.
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueBulk     = "bulk"
	QueueSearch   = "search"
	QueueEmail    = "email"
)

// migrateLockKey serializes concurrent Migrate callers against the same
// pool — see Migrate's own doc comment for why this is needed.
var migrateLockKey = db.AdvisoryLockKey("jobqueue.Migrate")

// Migrate applies River's own schema migrations against pool. Idempotent —
// safe to call on every startup, same as Store.Bootstrap elsewhere — but
// unlike Store.Bootstrap/SchemaSyncPool.Bootstrap, River's own migration
// DDL (CREATE TYPE/CREATE FUNCTION, no IF NOT EXISTS guard) isn't safe
// against two callers running it at the same time: two concurrent test
// packages both calling Migrate against the same dev Postgres instance hit
// "duplicate key value violates unique constraint" on Postgres's own
// pg_type/pg_proc catalogs. Guarded the same way goerp#171 guarded
// Store.Bootstrap — a transaction-scoped Postgres advisory lock
// (pg_advisory_xact_lock) held for the duration of the migration, so a
// second concurrent caller blocks until the first commits or rolls back
// rather than racing it.
//
// The lock is held on its own connection, opened directly rather than
// acquired from pool: an earlier version of this function used
// pool.Acquire, but that connection sits idle for the whole migration
// while riverpgxv5.New(pool) draws its own connections from that same
// pool to actually run it — on a small pool (a constrained CI runner, a
// caller-supplied pool sized for one purpose) that idle reservation can
// starve the migration of a connection to run on at all, deadlocking
// until the test framework's timeout kills it. A connection opened
// outside pool's own accounting can't compete with pool for capacity.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	lockConn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		return fmt.Errorf("open migration lock connection: %w", err)
	}
	defer func() { _ = lockConn.Close(ctx) }()

	tx, err := lockConn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration lock transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrateLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("create river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("apply river migrations: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration lock transaction: %w", err)
	}

	return nil
}

// New builds a River client against pool with all 5 queues and their
// per-queue concurrency from cfg. workers must be registered for every job
// type the client should work (river.NewWorkers + river.AddWorker), or
// left empty for an insert-only client that never calls Start.
//
// pool is expected to be PgBouncer-pooled (like Store.Bootstrap elsewhere),
// so River falls back to polling rather than LISTEN/NOTIFY — a latency
// tradeoff, not a correctness one.
func New(pool *pgxpool.Pool, cfg *config.Config, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			QueueCritical: {MaxWorkers: withDefault(cfg.QueueCriticalConcurrency, 5)},
			QueueDefault:  {MaxWorkers: withDefault(cfg.QueueDefaultConcurrency, 10)},
			QueueBulk:     {MaxWorkers: withDefault(cfg.QueueBulkConcurrency, 20)},
			QueueSearch:   {MaxWorkers: withDefault(cfg.QueueSearchConcurrency, 5)},
			QueueEmail:    {MaxWorkers: withDefault(cfg.QueueEmailConcurrency, 5)},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("create river client: %w", err)
	}
	return client, nil
}

// withDefault mirrors config.go's own envDefault for each field — needed
// because River requires MaxWorkers >= 1, and a Config built by hand
// (rather than via config.Load) leaves unset fields at zero.
func withDefault(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}
