// Package jobqueue is the engine's River-backed background job queue
// (erp-design.md §7.1): 5 named queues, per-queue concurrency, and the
// idempotency-key convention job types should follow (see ProbeArgs).
// Wiring into engine.New and real job types belongs to later tickets.
package jobqueue

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
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

// Migrate applies River's own schema migrations against pool. Idempotent —
// safe to call on every startup, same as Store.Bootstrap elsewhere.
func Migrate(ctx context.Context, pool *sql.DB) error {
	migrator, err := rivermigrate.New(riverdatabasesql.New(pool), nil)
	if err != nil {
		return fmt.Errorf("create river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("apply river migrations: %w", err)
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
func New(pool *sql.DB, cfg *config.Config, workers *river.Workers) (*river.Client[*sql.Tx], error) {
	client, err := river.NewClient(riverdatabasesql.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			QueueCritical: {MaxWorkers: cfg.QueueCriticalConcurrency},
			QueueDefault:  {MaxWorkers: cfg.QueueDefaultConcurrency},
			QueueBulk:     {MaxWorkers: cfg.QueueBulkConcurrency},
			QueueSearch:   {MaxWorkers: cfg.QueueSearchConcurrency},
			QueueEmail:    {MaxWorkers: cfg.QueueEmailConcurrency},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("create river client: %w", err)
	}
	return client, nil
}
